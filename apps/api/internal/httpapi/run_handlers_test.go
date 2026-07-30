package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	agentpkg "agentflow-platform/apps/api/internal/agent"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/store"
)

func TestCancelRunHandlerCancelsQueuedRun(t *testing.T) {
	fileStore, run := createHTTPTestRun(t)
	runtime := agentpkg.NewRuntime(agentpkg.RuntimeOptions{Store: fileStore, ModelClient: newLocalFallbackOpenAIClientForTest()})
	handler := &Handler{store: fileStore, agentRuntime: runtime}
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

func TestListCollaborationStepsHandler(t *testing.T) {
	fileStore, run := createHTTPTestRun(t)
	conversation, ok, err := fileStore.GetConversation(run.ConversationID)
	if err != nil || !ok {
		t.Fatalf("get conversation: ok=%t err=%v", ok, err)
	}
	step, err := fileStore.CreateCollaborationStep(domain.CollaborationStep{
		RunID: run.ID, ConversationID: conversation.ID, AgentID: run.AgentID, Role: "plan", Input: "task",
	})
	if err != nil {
		t.Fatalf("create collaboration step: %v", err)
	}
	handler := &Handler{store: fileStore}
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

func createHTTPTestRun(t *testing.T) (*store.FileStore, domain.Run) {
	t.Helper()
	fileStore, err := store.NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	conversation, err := fileStore.CreateConversation("handler coverage")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	run, err := fileStore.CreateRun("agent_planner", conversation.ID, testRuntimeSnapshot())
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	return fileStore, run
}
