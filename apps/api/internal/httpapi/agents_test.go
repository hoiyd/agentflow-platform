package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/openai"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/temporalrun"
)

type fakeTemporalRunner struct {
	started temporalrun.AgentRunWorkflowInput
}

func (f *fakeTemporalRunner) StartAgentRun(ctx context.Context, input temporalrun.AgentRunWorkflowInput) (string, string, error) {
	f.started = input
	return "agentflow-run-" + input.RunID, "workflow-run-test", nil
}

func (f *fakeTemporalRunner) CancelRun(ctx context.Context, workflowID string, workflowRunID string) error {
	return nil
}

func (f *fakeTemporalRunner) DescribeRunStatus(ctx context.Context, workflowID string, workflowRunID string) (string, error) {
	return temporalrun.WorkflowStatusRunning, nil
}

func TestUpdateAgentConfigAPI(t *testing.T) {
	fileStore, err := store.NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	handler := &Handler{store: fileStore}

	body := []byte(`{
		"name": "Configurable Agent",
		"description": "Updated description.",
		"system_prompt": "Use a concise style.",
		"tools": ["calculator"],
		"memory_enabled": false,
		"retrieval_enabled": true,
		"executor": "langchaingo"
	}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/agents/agent_planner", bytes.NewReader(body))
	recorder := httptest.NewRecorder()
	handler.updateAgent(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected update status 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	var agent domain.Agent
	if err := json.Unmarshal(recorder.Body.Bytes(), &agent); err != nil {
		t.Fatalf("decode agent: %v", err)
	}
	if agent.Name != "Configurable Agent" || agent.SystemPrompt != "Use a concise style." {
		t.Fatalf("expected updated text fields, got %#v", agent)
	}
	if agent.MemoryEnabled || !agent.RetrievalEnabled || agent.Executor != "langchaingo" {
		t.Fatalf("expected updated runtime config, got %#v", agent)
	}
	if len(agent.Tools) != 1 || agent.Tools[0] != "calculator" {
		t.Fatalf("expected updated tools, got %#v", agent.Tools)
	}

	persisted, ok, err := fileStore.GetAgent("agent_planner")
	if err != nil {
		t.Fatalf("get agent: %v", err)
	}
	if !ok {
		t.Fatal("expected updated agent")
	}
	if persisted.MemoryEnabled || !persisted.RetrievalEnabled || persisted.Executor != "langchaingo" {
		t.Fatalf("expected persisted config, got %#v", persisted)
	}
}

func TestCreateAgentConfigAPI(t *testing.T) {
	fileStore, err := store.NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	handler := &Handler{store: fileStore}

	body := []byte(`{
		"name": "Resume Reviewer",
		"description": "Reviews resumes against job descriptions.",
		"system_prompt": "Review resume evidence.",
		"tools": ["mock_web_search"],
		"memory_enabled": true,
		"retrieval_enabled": true,
		"executor": "native"
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/agents", bytes.NewReader(body))
	recorder := httptest.NewRecorder()
	handler.createAgent(recorder, req)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("expected create status 201, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	var agent domain.Agent
	if err := json.Unmarshal(recorder.Body.Bytes(), &agent); err != nil {
		t.Fatalf("decode agent: %v", err)
	}
	if agent.ID == "" || agent.Name != "Resume Reviewer" {
		t.Fatalf("expected created agent, got %#v", agent)
	}
	if !agent.MemoryEnabled || !agent.RetrievalEnabled || agent.Executor != domain.DefaultAgentExecutor {
		t.Fatalf("expected created runtime config, got %#v", agent)
	}
}

func TestArchiveAgentAPI(t *testing.T) {
	fileStore, err := store.NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	handler := &Handler{store: fileStore}

	created, err := fileStore.CreateAgent(domain.Agent{
		Name:         "Temporary Agent",
		Description:  "Can be archived.",
		SystemPrompt: "Help briefly.",
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/agents/"+created.ID, nil)
	recorder := httptest.NewRecorder()
	handler.archiveAgent(recorder, req)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("expected archive status 204, got %d body=%s", recorder.Code, recorder.Body.String())
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/agents", nil)
	listRecorder := httptest.NewRecorder()
	handler.listAgents(listRecorder, listReq)
	if listRecorder.Code != http.StatusOK {
		t.Fatalf("expected list status 200, got %d body=%s", listRecorder.Code, listRecorder.Body.String())
	}
	var agents []domain.Agent
	if err := json.Unmarshal(listRecorder.Body.Bytes(), &agents); err != nil {
		t.Fatalf("decode agents: %v", err)
	}
	for _, agent := range agents {
		if agent.ID == created.ID {
			t.Fatalf("expected archived agent to be hidden from list, got %#v", agent)
		}
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/agents/"+created.ID, nil)
	getRecorder := httptest.NewRecorder()
	handler.getAgent(getRecorder, getReq)
	if getRecorder.Code != http.StatusNotFound {
		t.Fatalf("expected archived agent get status 404, got %d body=%s", getRecorder.Code, getRecorder.Body.String())
	}
}

func TestTemporalAutonomousChatRequiresEnabledRuntime(t *testing.T) {
	fileStore, err := store.NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	handler := NewHandler(fileStore, openai.NewClient("", "", ""), nil, nil)

	body := []byte(`{"message":"run a durable demo","mode":"autonomous","runtime":"temporal"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/chat", bytes.NewReader(body))
	recorder := httptest.NewRecorder()
	handler.chat(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected SSE status 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "Temporal runtime is not enabled") {
		t.Fatalf("expected temporal disabled error, got body=%s", recorder.Body.String())
	}
}

func TestTemporalAutonomousChatStoresWorkflowMetadata(t *testing.T) {
	fileStore, err := store.NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	fakeRunner := &fakeTemporalRunner{}
	handler := NewHandler(fileStore, openai.NewClient("", "", ""), nil, nil).WithTemporalRunner(fakeRunner)

	body := []byte(`{"message":"run a durable demo","mode":"autonomous","runtime":"temporal"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/chat", bytes.NewReader(body))
	recorder := httptest.NewRecorder()
	handler.chat(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected SSE status 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if fakeRunner.started.RunID == "" {
		t.Fatalf("expected temporal workflow to be started")
	}

	run, ok, err := fileStore.GetRun(fakeRunner.started.RunID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if !ok {
		t.Fatal("expected run to be stored")
	}
	if run.Runtime != temporalrun.RuntimeTemporal || run.WorkflowID == "" || run.WorkflowRunID == "" || run.WorkflowStatus != temporalrun.WorkflowStatusRunning {
		t.Fatalf("expected temporal metadata on run, got %#v", run)
	}

	replayReq := httptest.NewRequest(http.MethodGet, "/api/runs/"+run.ID+"/replay", nil)
	replayRecorder := httptest.NewRecorder()
	handler.getRunReplay(replayRecorder, replayReq)
	if replayRecorder.Code != http.StatusOK {
		t.Fatalf("expected replay status 200, got %d body=%s", replayRecorder.Code, replayRecorder.Body.String())
	}
	var replay domain.RunReplay
	if err := json.Unmarshal(replayRecorder.Body.Bytes(), &replay); err != nil {
		t.Fatalf("decode replay: %v", err)
	}
	if replay.Run.Runtime != temporalrun.RuntimeTemporal || replay.Run.WorkflowID != run.WorkflowID {
		t.Fatalf("expected replay to expose workflow metadata, got %#v", replay.Run)
	}
}

func TestResumeCanceledTemporalRunStartsNewWorkflow(t *testing.T) {
	fileStore, err := store.NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	fakeRunner := &fakeTemporalRunner{}
	handler := NewHandler(fileStore, openai.NewClient("", "", ""), nil, nil).WithTemporalRunner(fakeRunner)

	conversation, err := fileStore.CreateConversation("Resume canceled temporal run")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	run, err := fileStore.CreateRun("agent_planner", conversation.ID)
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	if _, err := fileStore.UpdateRunRuntime(run.ID, temporalrun.RuntimeTemporal, "agentflow-run-"+run.ID, "old-workflow-run", temporalrun.WorkflowStatusCanceled); err != nil {
		t.Fatalf("set temporal metadata: %v", err)
	}
	if _, err := fileStore.UpdateRunStatus(run.ID, domain.RunCanceled, "canceled by user"); err != nil {
		t.Fatalf("cancel run: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/runs/"+run.ID+"/resume", bytes.NewReader([]byte(`{"user_input":"continue from the cancellation point"}`)))
	recorder := httptest.NewRecorder()
	handler.resumeRun(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected resume status 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if !fakeRunner.started.ResumeCanceled {
		t.Fatalf("expected temporal resume workflow to be started with ResumeCanceled=true")
	}
	updated, ok, err := fileStore.GetRun(run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if !ok {
		t.Fatal("expected run")
	}
	if updated.WorkflowRunID != "workflow-run-test" || updated.WorkflowStatus != temporalrun.WorkflowStatusRunning {
		t.Fatalf("expected updated workflow metadata, got %#v", updated)
	}
	if !strings.Contains(recorder.Body.String(), `"status":"running"`) {
		t.Fatalf("expected running SSE status, got %s", recorder.Body.String())
	}
}

func TestArchiveDefaultAgentAPIRejects(t *testing.T) {
	fileStore, err := store.NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	handler := &Handler{store: fileStore}

	req := httptest.NewRequest(http.MethodDelete, "/api/agents/agent_planner", nil)
	recorder := httptest.NewRecorder()
	handler.archiveAgent(recorder, req)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected default archive status 400, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}
