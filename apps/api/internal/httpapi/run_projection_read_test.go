package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/store"
)

type projectionReadStore struct {
	store.Store
	fail string
}
type projectionReadWorkspace struct {
	store.WorkspaceStore
	fail string
}

func (s projectionReadStore) ForWorkspace(scope domain.WorkspaceScope) store.WorkspaceStore {
	return projectionReadWorkspace{s.Store.ForWorkspace(scope), s.fail}
}

func (s projectionReadWorkspace) ListRunEvents(id string) ([]domain.RunEvent, error) {
	if s.fail == "events" {
		return nil, errors.New("events unavailable")
	}
	return s.WorkspaceStore.ListRunEvents(id)
}
func (s projectionReadWorkspace) GetRunUsageLedger(id string) (domain.RunUsageLedger, bool, error) {
	if s.fail == "ledger" {
		return domain.RunUsageLedger{}, false, errors.New("ledger unavailable")
	}
	if s.fail == "missing ledger" {
		return domain.RunUsageLedger{}, false, nil
	}
	return s.WorkspaceStore.GetRunUsageLedger(id)
}
func (s projectionReadWorkspace) ListVerificationEvidence(id string) ([]domain.VerificationEvidence, error) {
	if s.fail == "evidence" {
		return nil, errors.New("evidence unavailable")
	}
	return s.WorkspaceStore.ListVerificationEvidence(id)
}
func (s projectionReadWorkspace) GetRunReplay(string) (domain.RunReplay, bool, error) {
	return domain.RunReplay{}, false, errors.New("projection must not load full Replay")
}

func TestProjectionRecoveryDoesNotReadFullReplay(t *testing.T) {
	fixture, run := createHTTPTestRun(t)
	for _, typ := range []domain.RunEventType{domain.EventRunCreated, domain.EventRunStarted, domain.EventRunCompleted} {
		if _, err := fixture.CreateRunEvent(domain.RunEvent{RunID: run.ID, ConversationID: run.ConversationID, Type: typ}); err != nil {
			t.Fatal(err)
		}
	}
	replay, ok, err := fixture.GetRunReplay(run.ID)
	if err != nil || !ok {
		t.Fatalf("Replay found=%v err=%v", ok, err)
	}
	if _, err := fixture.UpdateRunStatus(run.ID, domain.RunCompleted, ""); err != nil {
		t.Fatal(err)
	}
	replay.Run.Status = domain.RunCompleted
	replay, _, _ = fixture.GetRunReplay(run.ID)
	h := &Handler{store: projectionReadStore{Store: fixture}, runEvents: event.NewHub(4)}
	if err := h.enrichRunProjection(fixture.ForWorkspace(domain.NewWorkspaceScope(run.WorkspaceID)), &replay); err != nil {
		t.Fatal(err)
	}
	for _, endpoint := range []string{"projection", "events"} {
		t.Run(endpoint, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/runs/"+run.ID+"/"+endpoint+"?after=1", nil)
			req.SetPathValue("id", run.ID)
			rec := httptest.NewRecorder()
			if endpoint == "projection" {
				h.getRunProjection(rec, req)
			} else {
				h.observeRunEvents(rec, req)
			}
			if rec.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			if endpoint == "projection" {
				var actual domain.RunProjectionSnapshot
				if err := json.Unmarshal(rec.Body.Bytes(), &actual); err != nil {
					t.Fatal(err)
				}
				encoded, _ := json.Marshal(replay.Projection)
				var expected domain.RunProjectionSnapshot
				_ = json.Unmarshal(encoded, &expected)
				if !reflect.DeepEqual(expected, actual) {
					t.Fatalf("projection differs from Replay: expected=%#v actual=%#v", expected, actual)
				}
			}
		})
	}
}

func TestProjectionDependencyFailuresAreNotSilentlyDropped(t *testing.T) {
	fixture, run := createHTTPTestRun(t)
	for _, fail := range []string{"events", "ledger", "missing ledger", "evidence"} {
		t.Run(fail, func(t *testing.T) {
			h := &Handler{store: projectionReadStore{Store: fixture, fail: fail}}
			rec := httptest.NewRecorder()
			h.getRunProjection(rec, httptest.NewRequest(http.MethodGet, "/api/runs/"+run.ID+"/projection", nil))
			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}
