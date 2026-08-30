package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/store"
)

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
			"executor": "retired-framework"
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
	if agent.MemoryEnabled || !agent.RetrievalEnabled || agent.Executor != domain.DefaultAgentExecutor {
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
	if persisted.MemoryEnabled || !persisted.RetrievalEnabled || persisted.Executor != domain.DefaultAgentExecutor {
		t.Fatalf("expected persisted config, got %#v", persisted)
	}
}

func TestRunUsageAPIExposesLedger(t *testing.T) {
	fileStore, err := store.NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	conversation, err := fileStore.CreateConversation("usage api")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	snapshot := testRuntimeSnapshot()
	snapshot.RunBudget = &domain.RuntimeRunBudget{MaxToolCalls: 3}
	run, err := fileStore.CreateRunWithContract("agent_planner", conversation.ID, snapshot, nil)
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	if _, _, err = fileStore.ApplyRunUsage(domain.RunUsageEntry{
		ID: "usage-1", RunID: run.ID, OperationID: "tool-1", Kind: domain.UsageToolExecution,
		Purpose: domain.UsagePurposePrimary, ToolName: "calculator", ToolCalls: 1, Timestamp: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("record usage: %v", err)
	}
	recorder := httptest.NewRecorder()
	(&Handler{store: fileStore}).getRunUsage(recorder, httptest.NewRequest(http.MethodGet, "/api/runs/"+run.ID+"/usage", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("usage endpoint status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var ledger domain.RunUsageLedger
	if err := json.Unmarshal(recorder.Body.Bytes(), &ledger); err != nil {
		t.Fatalf("decode ledger: %v", err)
	}
	if ledger.RunID != run.ID || ledger.Budget.MaxToolCalls != 3 || ledger.Totals.ToolCalls != 1 || len(ledger.Entries) != 1 {
		t.Fatalf("unexpected usage response: %#v", ledger)
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
		"tools": ["get_current_time"],
		"memory_enabled": true,
			"retrieval_enabled": true
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

func TestCreateAgentRejectsUnavailableTool(t *testing.T) {
	fileStore, err := store.NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	handler := &Handler{store: fileStore}
	body := []byte(`{
		"name": "Invalid tool agent",
		"system_prompt": "Use tools.",
		"tools": ["removed_tool"]
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/agents", bytes.NewReader(body))
	recorder := httptest.NewRecorder()
	handler.createAgent(recorder, req)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d body=%s", recorder.Code, recorder.Body.String())
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

func TestRunAPIReturnsSnapshotOnlyFromReplay(t *testing.T) {
	fileStore, err := store.NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	conversation, err := fileStore.CreateConversation("Snapshot API boundary")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	snapshot := testRuntimeSnapshot()
	snapshot.Agent.SystemPrompt = "private frozen prompt"
	run, err := fileStore.CreateRunWithContract("agent_planner", conversation.ID, snapshot, nil)
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	handler := &Handler{store: fileStore}

	for _, path := range []string{"/api/runs", "/api/runs/" + run.ID} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, path, nil)
		if path == "/api/runs" {
			handler.listRuns(recorder, request)
		} else {
			handler.getRun(recorder, request)
		}
		if recorder.Code != http.StatusOK {
			t.Fatalf("expected %s status 200, got %d", path, recorder.Code)
		}
		if bytes.Contains(recorder.Body.Bytes(), []byte("runtime_snapshot")) || bytes.Contains(recorder.Body.Bytes(), []byte("private frozen prompt")) {
			t.Fatalf("expected %s to omit runtime snapshot, got %s", path, recorder.Body.String())
		}
	}

	recorder := httptest.NewRecorder()
	handler.getRunReplay(recorder, httptest.NewRequest(http.MethodGet, "/api/runs/"+run.ID+"/replay", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected replay status 200, got %d", recorder.Code)
	}
	var replay map[string]json.RawMessage
	if err := json.Unmarshal(recorder.Body.Bytes(), &replay); err != nil {
		t.Fatalf("decode replay: %v", err)
	}
	if _, ok := replay["runtime_snapshot"]; !ok {
		t.Fatalf("expected replay snapshot, got %s", recorder.Body.String())
	}
	var replayRun map[string]json.RawMessage
	if err := json.Unmarshal(replay["run"], &replayRun); err != nil {
		t.Fatalf("decode replay run: %v", err)
	}
	if _, ok := replayRun["runtime_snapshot"]; ok {
		t.Fatalf("expected replay run to omit duplicate snapshot, got %s", replay["run"])
	}
}
