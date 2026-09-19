package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/event"
)

type eventStreamRecorder struct {
	*httptest.ResponseRecorder
	flushed chan struct{}
	once    sync.Once
}

func (r *eventStreamRecorder) Flush() { r.once.Do(func() { close(r.flushed) }) }

func TestObserveRunEventsReplaysAfterCursorThenReturnsSnapshot(t *testing.T) {
	fixtureStore, run := createHTTPTestRun(t)
	if _, err := fixtureStore.CreateRunEvent(domain.RunEvent{
		Type: domain.EventRunCreated, RunID: run.ID, ConversationID: run.ConversationID,
		Payload: map[string]any{"status": domain.RunQueued},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixtureStore.CreateRunEvent(domain.RunEvent{
		Type: domain.EventRunCompleted, RunID: run.ID, ConversationID: run.ConversationID,
		Payload: map[string]any{"status": domain.RunCompleted},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixtureStore.UpdateRunStatus(run.ID, domain.RunCompleted, ""); err != nil {
		t.Fatal(err)
	}

	handler := &Handler{store: fixtureStore, runEvents: event.NewHub(4)}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/runs/"+run.ID+"/events?after=1", nil)
	request.SetPathValue("id", run.ID)
	handler.observeRunEvents(recorder, request)

	body := recorder.Body.String()
	if recorder.Code != http.StatusOK || !strings.Contains(body, "id: 2\nevent: run.completed") ||
		strings.Contains(body, "id: 1\n") || !strings.Contains(body, "event: run.snapshot") {
		t.Fatalf("unexpected event stream: status=%d body=%s", recorder.Code, body)
	}
	parts := strings.Split(body, "event: run.snapshot\ndata: ")
	if len(parts) != 2 {
		t.Fatalf("snapshot frame missing: %s", body)
	}
	var snapshot domain.RunProjectionSnapshot
	if err := json.Unmarshal([]byte(strings.TrimSpace(parts[1])), &snapshot); err != nil || snapshot.AsOfSequence != 2 || snapshot.Run.Status != domain.RunCompleted {
		t.Fatalf("snapshot=%#v err=%v", snapshot, err)
	}
}

func TestObserveRunEventsValidatesCursorAndRun(t *testing.T) {
	fixtureStore, run := createHTTPTestRun(t)
	handler := &Handler{store: fixtureStore, runEvents: event.NewHub(2)}

	invalid := httptest.NewRecorder()
	invalidRequest := httptest.NewRequest(http.MethodGet, "/api/runs/run-1/events?after=-1", nil)
	invalidRequest.SetPathValue("id", "run-1")
	handler.observeRunEvents(invalid, invalidRequest)
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid cursor status=%d", invalid.Code)
	}

	ahead := httptest.NewRecorder()
	aheadRequest := httptest.NewRequest(http.MethodGet, "/api/runs/"+run.ID+"/events?after=1", nil)
	aheadRequest.SetPathValue("id", run.ID)
	handler.observeRunEvents(ahead, aheadRequest)
	if ahead.Code != http.StatusConflict {
		t.Fatalf("ahead cursor status=%d", ahead.Code)
	}

	missing := httptest.NewRecorder()
	missingRequest := httptest.NewRequest(http.MethodGet, "/api/runs/missing/events", nil)
	missingRequest.SetPathValue("id", "missing")
	handler.observeRunEvents(missing, missingRequest)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing run status=%d", missing.Code)
	}
}

func TestObserveRunEventsReportsReplayFailureBeforeStreaming(t *testing.T) {
	httpStore, workspace, _ := newBoundaryHTTPStore(t)
	workspace.getRunReplayErr = errors.New("replay unavailable")
	handler := &Handler{store: httpStore, runEvents: event.NewHub(2)}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/runs/run-1/events", nil)
	request.SetPathValue("id", "run-1")

	handler.observeRunEvents(recorder, request)

	if recorder.Code != http.StatusInternalServerError || !strings.Contains(recorder.Body.String(), "Internal Server Error") {
		t.Fatalf("replay failure status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestObserveRunEventsContinuesFromSnapshotIntoLiveEvents(t *testing.T) {
	fixtureStore, run := createHTTPTestRun(t)
	started, err := fixtureStore.CreateRunEvent(domain.RunEvent{
		Type: domain.EventRunStarted, RunID: run.ID, ConversationID: run.ConversationID,
		Payload: map[string]any{"status": domain.RunRunning},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = fixtureStore.UpdateRunStatus(run.ID, domain.RunRunning, ""); err != nil {
		t.Fatal(err)
	}
	hub := event.NewHub(4)
	handler := &Handler{store: fixtureStore, runEvents: hub}
	recorder := &eventStreamRecorder{ResponseRecorder: httptest.NewRecorder(), flushed: make(chan struct{})}
	request := httptest.NewRequest(http.MethodGet, "/api/runs/"+run.ID+"/events?after="+strconv.FormatInt(started.Sequence, 10), nil)
	request.SetPathValue("id", run.ID)
	done := make(chan struct{})
	go func() {
		handler.observeRunEvents(recorder, request)
		close(done)
	}()
	<-recorder.flushed

	completed, err := fixtureStore.CreateRunEvent(domain.RunEvent{
		Type: domain.EventRunCompleted, RunID: run.ID, ConversationID: run.ConversationID,
		Payload: map[string]any{"status": domain.RunCompleted},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = fixtureStore.UpdateRunStatus(run.ID, domain.RunCompleted, ""); err != nil {
		t.Fatal(err)
	}
	hub.PublishCommitted(completed)
	<-done

	body := recorder.Body.String()
	if strings.Index(body, "event: run.snapshot") > strings.Index(body, "id: 2\nevent: run.completed") {
		t.Fatalf("live event did not follow snapshot: %s", body)
	}
}

func TestRunEventCursorPrefersLastEventID(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/?after=3", nil)
	request.Header.Set("Last-Event-ID", "7")
	cursor, err := runEventCursor(request)
	if err != nil || cursor != 7 {
		t.Fatalf("cursor=%d err=%v", cursor, err)
	}
}
