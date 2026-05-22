package store

import (
	"os"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
)

func TestFileStoreSeedsDefaultAgents(t *testing.T) {
	store, err := NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	agents, err := store.ListAgents()
	if err != nil {
		t.Fatalf("list agents: %v", err)
	}
	if len(agents) != 4 {
		t.Fatalf("expected 4 default agents, got %d", len(agents))
	}

	agent, ok, err := store.GetDefaultAgent()
	if err != nil {
		t.Fatalf("get default agent: %v", err)
	}
	if !ok || agent.ID != "agent_planner" {
		t.Fatalf("expected planner default agent, got %#v", agent)
	}
}

func TestFileStoreMigratesDefaultAgentText(t *testing.T) {
	path := t.TempDir() + "/agentflow.json"
	now := time.Now().UTC()
	data := `{
  "conversations": [],
  "messages": [],
  "agents": [
    {
      "id": "agent_planner",
      "name": "Planner Agent",
      "description": "Breaks ambiguous requests into ordered plans and tracks next actions.",
      "system_prompt": "You are AgentFlow's Planner Agent. Convert goals into clear, ordered plans with dependencies, risks, and next actions.",
      "tools": ["get_current_time", "mock_web_search"],
      "created_at": "` + now.Format(time.RFC3339Nano) + `",
      "updated_at": "` + now.Format(time.RFC3339Nano) + `"
    }
  ],
  "runs": []
}`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatalf("write store fixture: %v", err)
	}

	store, err := NewFileStore(path)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	agent, ok, err := store.GetAgent("agent_planner")
	if err != nil {
		t.Fatalf("get agent: %v", err)
	}
	if !ok {
		t.Fatal("expected migrated agent")
	}
	if agent.Name != "Narrative Strategist" {
		t.Fatalf("expected migrated planner name, got %q", agent.Name)
	}
	if agent.Description == "Breaks ambiguous requests into ordered plans and tracks next actions." {
		t.Fatalf("expected migrated planner description, got %q", agent.Description)
	}
	if agent.SystemPrompt == "You are AgentFlow's Planner Agent. Convert goals into clear, ordered plans with dependencies, risks, and next actions." {
		t.Fatalf("expected migrated planner system prompt, got %q", agent.SystemPrompt)
	}
}

func TestFileStoreRunLifecycle(t *testing.T) {
	store, err := NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	conversation, err := store.CreateConversation("Runtime test")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}

	run, err := store.CreateRun("agent_planner", conversation.ID)
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	if run.Status != domain.RunQueued {
		t.Fatalf("expected queued run, got %s", run.Status)
	}

	run, err = store.UpdateRunStatus(run.ID, domain.RunRunning, "")
	if err != nil {
		t.Fatalf("mark running: %v", err)
	}
	if run.StartedAt == nil || run.Status != domain.RunRunning {
		t.Fatalf("expected running run with started_at, got %#v", run)
	}

	run, err = store.UpdateRunAgent(run.ID, "agent_coding")
	if err != nil {
		t.Fatalf("update run agent: %v", err)
	}
	if run.AgentID != "agent_coding" {
		t.Fatalf("expected updated run agent, got %q", run.AgentID)
	}

	run, err = store.UpdateRunStatus(run.ID, domain.RunCompleted, "")
	if err != nil {
		t.Fatalf("mark completed: %v", err)
	}
	if run.CompletedAt == nil || run.Status != domain.RunCompleted {
		t.Fatalf("expected completed run with completed_at, got %#v", run)
	}
}

func TestFileStoreDeleteConversationCascades(t *testing.T) {
	store, err := NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	conversation, err := store.CreateConversation("Delete me")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	if _, err := store.AddMessage(conversation.ID, "user", "hello"); err != nil {
		t.Fatalf("add message: %v", err)
	}
	run, err := store.CreateRun("agent_planner", conversation.ID)
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	if _, err := store.CreateCollaborationStep(domain.CollaborationStep{
		RunID:          run.ID,
		ConversationID: conversation.ID,
		Role:           "planner",
		Status:         domain.CollaborationStepCompleted,
		Input:          "plan",
		Output:         "done",
	}); err != nil {
		t.Fatalf("create collaboration step: %v", err)
	}

	if err := store.DeleteConversation(conversation.ID); err != nil {
		t.Fatalf("delete conversation: %v", err)
	}

	if _, ok, err := store.GetConversation(conversation.ID); err != nil {
		t.Fatalf("get conversation after delete: %v", err)
	} else if ok {
		t.Fatal("expected conversation to be removed")
	}

	messages, err := store.ListMessages(conversation.ID)
	if err != nil {
		t.Fatalf("list messages after delete: %v", err)
	}
	if len(messages) != 0 {
		t.Fatalf("expected no messages after delete, got %d", len(messages))
	}

	if _, ok, err := store.GetRun(run.ID); err != nil {
		t.Fatalf("get run after delete: %v", err)
	} else if ok {
		t.Fatal("expected run to be removed")
	}

	steps, err := store.ListCollaborationSteps(run.ID)
	if err != nil {
		t.Fatalf("list steps after delete: %v", err)
	}
	if len(steps) != 0 {
		t.Fatalf("expected no steps after delete, got %d", len(steps))
	}
}

func TestFileStoreCreateAgent(t *testing.T) {
	store, err := NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	agent, err := store.CreateAgent(domain.Agent{
		Name:         "QA Agent",
		Description:  "Checks answers.",
		SystemPrompt: "Be strict.",
		Tools:        []string{"calculator", "calculator", " mock_web_search "},
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	if agent.ID == "" {
		t.Fatal("expected generated agent id")
	}
	if len(agent.Tools) != 2 {
		t.Fatalf("expected normalized tools, got %#v", agent.Tools)
	}
}
