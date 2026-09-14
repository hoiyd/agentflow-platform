package httpapi

import "agentflow-platform/apps/api/internal/testsupport/fixturestore"

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	agentpkg "agentflow-platform/apps/api/internal/agent"
	"agentflow-platform/apps/api/internal/domain"
)

func TestCancelRunHandlerCancelsQueuedRun(t *testing.T) {
	fixtureStore, run := createHTTPTestRun(t)
	runtime := agentpkg.NewRuntime(agentpkg.RuntimeOptions{Store: fixtureStore, ModelClient: newLocalFallbackOpenAIClientForTest()})
	handler := &Handler{store: fixtureStore, agentRuntime: runtime}
	recorder := httptest.NewRecorder()

	handler.cancelRun(recorder, httptest.NewRequest(http.MethodPost, "/api/runs/"+run.ID+"/cancel", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("cancel run status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var canceled domain.Run
	if err := json.Unmarshal(recorder.Body.Bytes(), &canceled); err != nil {
		t.Fatalf("decode canceled run: %v", err)
	}
	if canceled.Status != domain.RunCanceled || canceled.RuntimeSnapshot != nil {
		t.Fatalf("unexpected canceled run: %#v", canceled)
	}
}

func TestDelegatedChildRunMutationsRequireParent(t *testing.T) {
	fixtureStore, parent := createHTTPTestRun(t)
	child := createHTTPChildRun(t, fixtureStore, parent)
	runtime := agentpkg.NewRuntime(agentpkg.RuntimeOptions{Store: fixtureStore, ModelClient: newLocalFallbackOpenAIClientForTest()})
	handler := &Handler{store: fixtureStore, agentRuntime: runtime}

	requests := []struct {
		name   string
		handle http.HandlerFunc
		req    *http.Request
	}{
		{name: "cancel", handle: handler.cancelRun, req: httptest.NewRequest(http.MethodPost, "/api/runs/"+child.ID+"/cancel", nil)},
		{name: "resume", handle: handler.resumeRun, req: httptest.NewRequest(http.MethodPost, "/api/runs/"+child.ID+"/resume", bytes.NewBufferString(`{}`))},
	}
	for _, test := range requests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			test.handle(recorder, test.req)
			if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), parent.ID) {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}

	unchanged, ok, err := fixtureStore.GetRun(child.ID)
	if err != nil || !ok || unchanged.Status != domain.RunQueued {
		t.Fatalf("child mutation escaped parent boundary: run=%#v ok=%t err=%v", unchanged, ok, err)
	}
}

func TestListCollaborationStepsHandler(t *testing.T) {
	fixtureStore, run := createHTTPTestRun(t)
	conversation, ok, err := fixtureStore.GetConversation(run.ConversationID)
	if err != nil || !ok {
		t.Fatalf("get conversation: ok=%t err=%v", ok, err)
	}
	step, err := fixtureStore.CreateCollaborationStep(domain.CollaborationStep{
		RunID: run.ID, ConversationID: conversation.ID, AgentID: run.AgentID, Role: "plan", Input: "task",
	})
	if err != nil {
		t.Fatalf("create collaboration step: %v", err)
	}
	handler := &Handler{store: fixtureStore}
	recorder := httptest.NewRecorder()

	handler.listCollaborationSteps(recorder, httptest.NewRequest(http.MethodGet, "/api/runs/"+run.ID+"/collaboration_steps", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("list steps status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var steps []domain.CollaborationStep
	if err := json.Unmarshal(recorder.Body.Bytes(), &steps); err != nil || len(steps) != 1 || steps[0].ID != step.ID {
		t.Fatalf("unexpected steps: items=%#v err=%v", steps, err)
	}

	missingRecorder := httptest.NewRecorder()
	handler.listCollaborationSteps(missingRecorder, httptest.NewRequest(http.MethodGet, "/api/runs/missing/collaboration_steps", nil))
	if missingRecorder.Code != http.StatusNotFound {
		t.Fatalf("expected missing run status 404, got %d", missingRecorder.Code)
	}
}

func TestGetRunProjectionReturnsCanonicalWatermarkAndDiagnostics(t *testing.T) {
	fixtureStore, run := createHTTPTestRun(t)
	if _, err := fixtureStore.CreateRunEvent(domain.RunEvent{Type: domain.EventRunCreated, RunID: run.ID, ConversationID: run.ConversationID}); err != nil {
		t.Fatal(err)
	}
	handler := &Handler{store: fixtureStore}
	recorder := httptest.NewRecorder()

	handler.getRunProjection(recorder, httptest.NewRequest(http.MethodGet, "/api/runs/"+run.ID+"/projection", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("projection status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var projection domain.RunProjectionSnapshot
	if err := json.Unmarshal(recorder.Body.Bytes(), &projection); err != nil {
		t.Fatal(err)
	}
	if projection.AsOfSequence != 1 || projection.Run.RunID != run.ID || projection.InvariantFailures == nil {
		t.Fatalf("unexpected projection: %#v", projection)
	}
}

func TestRunProjectionAlwaysReturnsInvariantDiagnostics(t *testing.T) {
	fixtureStore, run := createHTTPTestRun(t)
	if _, err := fixtureStore.CreateRunEvent(domain.RunEvent{
		Type: domain.EventToolFailed, RunID: run.ID, Payload: map[string]any{"tool_call_id": "orphan-call"},
	}); err != nil {
		t.Fatal(err)
	}
	handler := &Handler{store: fixtureStore}
	recorder := httptest.NewRecorder()
	handler.getRunProjection(recorder, httptest.NewRequest(http.MethodGet, "/api/runs/"+run.ID+"/projection", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var snapshot domain.RunProjectionSnapshot
	if err := json.Unmarshal(recorder.Body.Bytes(), &snapshot); err != nil || len(snapshot.InvariantFailures) != 1 || snapshot.InvariantFailures[0].Code != "tool_terminal_orphan" {
		t.Fatalf("diagnostics=%#v err=%v", snapshot.InvariantFailures, err)
	}
}

func TestRunReplayAlwaysReturnsInvariantDiagnostics(t *testing.T) {
	fixtureStore, run := createHTTPTestRun(t)
	if _, err := fixtureStore.CreateRunEvent(domain.RunEvent{
		Type: domain.EventToolFailed, RunID: run.ID, Payload: map[string]any{"tool_call_id": "orphan-call"},
	}); err != nil {
		t.Fatal(err)
	}
	handler := &Handler{store: fixtureStore}
	recorder := httptest.NewRecorder()
	handler.getRunReplay(recorder, httptest.NewRequest(http.MethodGet, "/api/runs/"+run.ID+"/replay", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var replay domain.RunReplay
	if err := json.Unmarshal(recorder.Body.Bytes(), &replay); err != nil || len(replay.Projection.InvariantFailures) != 1 || replay.Projection.InvariantFailures[0].Code != "tool_terminal_orphan" {
		t.Fatalf("diagnostics=%#v err=%v", replay.Projection.InvariantFailures, err)
	}
}

func TestGetRunProjectionHandlesInvalidMissingAndStoreFailures(t *testing.T) {
	t.Run("missing id", func(t *testing.T) {
		fixtureStore, _ := createHTTPTestRun(t)
		recorder := httptest.NewRecorder()
		(&Handler{store: fixtureStore}).getRunProjection(recorder, httptest.NewRequest(http.MethodGet, "/api/runs//projection", nil))
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("unknown run", func(t *testing.T) {
		fixtureStore, _ := createHTTPTestRun(t)
		recorder := httptest.NewRecorder()
		(&Handler{store: fixtureStore}).getRunProjection(recorder, httptest.NewRequest(http.MethodGet, "/api/runs/missing/projection", nil))
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("replay failure", func(t *testing.T) {
		httpStore, workspace, _ := newBoundaryHTTPStore(t)
		workspace.getRunReplayErr = errors.New("replay unavailable")
		recorder := httptest.NewRecorder()
		(&Handler{store: httpStore}).getRunProjection(recorder, httptest.NewRequest(http.MethodGet, "/api/runs/run-1/projection", nil))
		if recorder.Code != http.StatusInternalServerError {
			t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("invariant dependency failure", func(t *testing.T) {
		fixtureStore, run := createHTTPTestRun(t)
		wrapped := &modelRequestHTTPStore{Store: fixtureStore, recordsErr: errors.New("model requests unavailable")}
		recorder := httptest.NewRecorder()
		(&Handler{store: wrapped}).getRunProjection(recorder, httptest.NewRequest(http.MethodGet, "/api/runs/"+run.ID+"/projection", nil))
		if recorder.Code != http.StatusInternalServerError {
			t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
		}
	})
}

func createHTTPTestRun(t *testing.T) (*fixturestore.Store, domain.Run) {
	t.Helper()
	fixtureStore := fixturestore.New()

	conversation, err := fixtureStore.CreateConversation("handler coverage")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	run, err := fixtureStore.CreateRunWithContract("agent_planner", conversation.ID, testRuntimeSnapshot(), nil)
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	return fixtureStore, run
}

func createHTTPChildRun(t *testing.T, fixtureStore *fixturestore.Store, parent domain.Run) domain.Run {
	t.Helper()
	snapshot := testRuntimeSnapshot()
	snapshot.Mode = agentpkg.ChatModeSingle
	snapshot.AutonomousLimits = nil
	snapshot.Delegation = &domain.RuntimeDelegation{
		DelegationID: "delegation-http-child", ParentRunID: parent.ID, ParentTurnID: "turn-http-child",
		ParentStageID: "stage-http-child", Depth: 1, IsolatedContext: true,
		TimeoutMS: time.Minute.Milliseconds(), SummaryMaxChars: 100,
	}
	child, _, err := fixtureStore.CreateChildRun(domain.ChildRunRequest{
		Delegation: domain.RunDelegation{
			ID: snapshot.Delegation.DelegationID, ParentRunID: parent.ID, ParentTurnID: snapshot.Delegation.ParentTurnID,
			ParentStageID: snapshot.Delegation.ParentStageID, AgentID: snapshot.Agent.ID, Depth: 1,
			Task: "delegated work", TimeoutMS: snapshot.Delegation.TimeoutMS,
		},
		RuntimeSnapshot: snapshot,
	})
	if err != nil {
		t.Fatalf("create child run: %v", err)
	}
	return child
}
