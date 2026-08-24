package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
	eventpkg "agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/modelrequest"
	"agentflow-platform/apps/api/internal/requestcapture"
	"agentflow-platform/apps/api/internal/store"
)

func TestListModelRequestsHidesContentByDefaultAndReturnsManifest(t *testing.T) {
	fileStore, run := createHTTPTestRun(t)
	manifest := domain.ContextManifest{
		ID: "ctx-http", RunID: run.ID, ModelCallID: "call-http", Model: "test",
		Entries: []domain.ContextManifestEntry{
			{Source: "system", Selected: true, EstimatedTokens: 5},
			{Source: "history", Selected: false, EstimatedTokens: 3},
		},
	}
	if _, err := fileStore.CreateRunEvent(domain.RunEvent{
		Type: domain.EventContextAssembled, RunID: run.ID, ConversationID: run.ConversationID,
		Payload: map[string]any{"manifest": manifest},
	}); err != nil {
		t.Fatalf("create manifest: %v", err)
	}
	recorder := requestcapture.NewRecorder(fileStore, requestcapture.Options{Mode: domain.ModelRequestCaptureFull})
	ctx := eventpkg.WithScope(context.Background(), eventpkg.Scope{RunID: run.ID, ConversationID: run.ConversationID})
	payload := []byte(`{"model":"test","messages":[{"role":"user","content":"debug me"}]}`)
	if err := recorder.Record(ctx, modelrequest.Observation{
		ModelCallID: "call-http", Operation: "chat.completion", Provider: "local", Model: "test",
		ContextManifestID: manifest.ID, SourceTokenBreakdown: map[string]int{"system": 5}, Payload: payload,
	}); err != nil {
		t.Fatalf("record model request: %v", err)
	}
	handler := &Handler{store: fileStore}

	responseRecorder := httptest.NewRecorder()
	handler.listModelRequests(responseRecorder, httptest.NewRequest(http.MethodGet, "/api/runs/"+run.ID+"/model_requests", nil))
	if responseRecorder.Code != http.StatusOK {
		t.Fatalf("list requests status=%d body=%s", responseRecorder.Code, responseRecorder.Body.String())
	}
	var response modelRequestDebugResponse
	if err := json.Unmarshal(responseRecorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.ReconstructabilityStatus != "valid" || len(response.Records) != 1 ||
		response.Records[0].Capture.Content != "" || response.Records[0].Manifest == nil ||
		response.Records[0].Manifest.ID != manifest.ID || !response.Records[0].Capture.Reconstructable ||
		!response.Records[0].SourceDiff.MatchesEnvelope || response.Records[0].SourceDiff.ManifestExcludedTokens["history"] != 3 {
		t.Fatalf("unexpected metadata response: %#v", response)
	}

	contentRecorder := httptest.NewRecorder()
	handler.listModelRequests(contentRecorder, httptest.NewRequest(http.MethodGet, "/api/runs/"+run.ID+"/model_requests?include_content=true", nil))
	var withContent modelRequestDebugResponse
	_ = json.Unmarshal(contentRecorder.Body.Bytes(), &withContent)
	if len(withContent.Records) != 1 || withContent.Records[0].Capture.Content != string(payload) {
		t.Fatalf("explicit content response missing capture: %#v", withContent)
	}
}

func TestListModelRequestsEnforcesWorkspaceScope(t *testing.T) {
	fileStore, run := createHTTPTestRun(t)
	handler := &Handler{store: fileStore}
	request := httptest.NewRequest(http.MethodGet, "/api/runs/"+run.ID+"/model_requests", nil)
	request.Header.Set(WorkspaceHeader, "another-workspace")
	recorder := httptest.NewRecorder()

	handler.listModelRequests(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("expected workspace-scoped 404, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestListModelRequestsHandlesStoreFailuresAndInvalidProjection(t *testing.T) {
	fileStore, run := createHTTPTestRun(t)
	tests := []struct {
		name       string
		path       string
		store      *modelRequestHTTPStore
		wantStatus int
		wantBody   string
	}{
		{name: "missing id", path: "/api/runs//model_requests", store: &modelRequestHTTPStore{Store: fileStore}, wantStatus: http.StatusBadRequest, wantBody: "run id is required"},
		{name: "run lookup", path: "/api/runs/" + run.ID + "/model_requests", store: &modelRequestHTTPStore{Store: fileStore, runErr: errors.New("run lookup failed")}, wantStatus: http.StatusInternalServerError, wantBody: "run lookup failed"},
		{name: "record list", path: "/api/runs/" + run.ID + "/model_requests", store: &modelRequestHTTPStore{Store: fileStore, run: run, ok: true, recordsErr: errors.New("records failed")}, wantStatus: http.StatusInternalServerError, wantBody: "records failed"},
		{name: "event list", path: "/api/runs/" + run.ID + "/model_requests", store: &modelRequestHTTPStore{Store: fileStore, run: run, ok: true, eventsErr: errors.New("events failed")}, wantStatus: http.StatusInternalServerError, wantBody: "events failed"},
		{name: "invalid projection", path: "/api/runs/" + run.ID + "/model_requests", store: &modelRequestHTTPStore{
			Store: fileStore, run: run, ok: true, records: []domain.ModelRequestRecord{},
			events: []domain.RunEvent{{Type: domain.EventModelRequestPrepared, Payload: map[string]any{
				"record_id": "orphan", "model_call_id": "call", "attempt": 1, "payload_hash": "sha256:orphan",
			}}},
		}, wantStatus: http.StatusOK, wantBody: `"reconstructability_status":"invalid"`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler := &Handler{store: test.store}
			response := httptest.NewRecorder()
			handler.listModelRequests(response, httptest.NewRequest(http.MethodGet, test.path, nil))
			if response.Code != test.wantStatus || !strings.Contains(response.Body.String(), test.wantBody) {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestModelRequestManifestProjectionHelpers(t *testing.T) {
	manifest := domain.ContextManifest{ID: "ctx", Entries: []domain.ContextManifestEntry{
		{Source: "system", Selected: true, EstimatedTokens: 2},
		{Source: "history", Selected: false, EstimatedTokens: 3},
		{Source: "ignored", Selected: true, EstimatedTokens: 0},
	}}
	selected, excluded := manifestTokenBreakdown(manifest)
	if selected["system"] != 2 || excluded["history"] != 3 || len(selected) != 1 {
		t.Fatalf("unexpected breakdown: selected=%#v excluded=%#v", selected, excluded)
	}
	if equalIntMap(map[string]int{"system": 1}, map[string]int{}) ||
		equalIntMap(map[string]int{"system": 1}, map[string]int{"system": 2}) {
		t.Fatal("different source maps should not match")
	}

	events := []domain.RunEvent{
		{Type: domain.EventModelCompleted},
		{Type: domain.EventContextAssembled, Payload: map[string]any{"manifest": make(chan int)}},
		{Type: domain.EventContextAssembled, Payload: map[string]any{"manifest": "invalid"}},
		{Type: domain.EventContextAssembled, Payload: map[string]any{"manifest": domain.ContextManifest{}}},
		{Type: domain.EventContextAssembled, Payload: map[string]any{"manifest": manifest}},
	}
	manifests := modelRequestManifests(events)
	if len(manifests) != 1 || manifests[manifest.ID].ID != manifest.ID {
		t.Fatalf("unexpected manifests: %#v", manifests)
	}
}

type modelRequestHTTPStore struct {
	store.Store
	run        domain.Run
	ok         bool
	runErr     error
	records    []domain.ModelRequestRecord
	recordsErr error
	events     []domain.RunEvent
	eventsErr  error
}

func (s *modelRequestHTTPStore) ForWorkspace(scope domain.WorkspaceScope) store.WorkspaceStore {
	return &modelRequestWorkspaceStore{WorkspaceStore: s.Store.ForWorkspace(scope), parent: s}
}

type modelRequestWorkspaceStore struct {
	store.WorkspaceStore
	parent *modelRequestHTTPStore
}

func (s *modelRequestWorkspaceStore) GetRun(runID string) (domain.Run, bool, error) {
	parent := s.parent
	if parent.runErr != nil || parent.ok {
		return parent.run, parent.ok, parent.runErr
	}
	return s.WorkspaceStore.GetRun(runID)
}

func (s *modelRequestHTTPStore) GetRunInWorkspace(workspaceID, runID string) (domain.Run, bool, error) {
	if s.runErr != nil || s.ok {
		return s.run, s.ok, s.runErr
	}
	return s.Store.GetRunInWorkspace(workspaceID, runID)
}

func (s *modelRequestWorkspaceStore) ListModelRequestRecords(runID string) ([]domain.ModelRequestRecord, error) {
	parent := s.parent
	if parent.recordsErr != nil || parent.records != nil {
		return parent.records, parent.recordsErr
	}
	return s.WorkspaceStore.ListModelRequestRecords(runID)
}

func (s *modelRequestWorkspaceStore) ListRunEvents(runID string) ([]domain.RunEvent, error) {
	parent := s.parent
	if parent.eventsErr != nil || parent.events != nil {
		return parent.events, parent.eventsErr
	}
	return s.WorkspaceStore.ListRunEvents(runID)
}
