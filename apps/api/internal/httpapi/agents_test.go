package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

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
