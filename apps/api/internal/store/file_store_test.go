package store

import (
	"os"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
)

func TestFileStoreListsConversationEventsThroughRunOwnership(t *testing.T) {
	fileStore, err := NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	conversation, _ := fileStore.CreateConversation("Event history")
	run, err := fileStore.CreateRun("agent_planner", conversation.ID, testRuntimeSnapshot())
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	created, err := fileStore.CreateRunEvent(domain.RunEvent{RunID: run.ID, Type: domain.EventToolFailed, Payload: map[string]any{"error": "exact failure"}})
	if err != nil {
		t.Fatalf("create event: %v", err)
	}
	events, err := fileStore.ListConversationRunEvents(conversation.ID)
	if err != nil || len(events) != 1 || events[0].ID != created.ID {
		t.Fatalf("conversation event history: events=%#v err=%v", events, err)
	}
}

func TestFileStoreMessageCitationsRoundTrip(t *testing.T) {
	path := t.TempDir() + "/agentflow.json"
	first, err := NewFileStore(path)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	conversation, _ := first.CreateConversation("citation round trip")
	_, err = first.AddMessageWithCitations(conversation.ID, "assistant", "Answer [S1].", []domain.RAGCitation{{
		SourceID: "S1", DocumentID: "doc-1", DocumentTitle: "Guide", ChunkID: "chunk-1",
		SourceChunkIDs: []string{"chunk-1", "chunk-2"}, SectionPath: []string{"Deploy"},
	}})
	if err != nil {
		t.Fatalf("add cited message: %v", err)
	}
	reopened, err := NewFileStore(path)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	messages, err := reopened.ListMessages(conversation.ID)
	if err != nil || len(messages) != 1 || len(messages[0].Citations) != 1 || messages[0].Citations[0].SourceID != "S1" || len(messages[0].Citations[0].SourceChunkIDs) != 2 {
		t.Fatalf("citations did not round trip: messages=%#v err=%v", messages, err)
	}
}

func TestFileStoreContextCompactionRoundTrip(t *testing.T) {
	path := t.TempDir() + "/agentflow.json"
	first, err := NewFileStore(path)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	conversation, _ := first.CreateConversation("compaction round trip")
	run, _ := first.CreateRun("agent_planner", conversation.ID, testRuntimeSnapshot())
	created, err := first.CreateContextCompaction(domain.ContextCompaction{
		ConversationID: conversation.ID, RunID: run.ID, Trigger: "soft", Summary: "structured summary",
		SourceMessageIDs: []string{"msg-1", "msg-2"}, SourceHash: "hash-1", BeforeTokens: 100,
		AfterTokens: 25, SummaryModel: "test", AlgorithmVersion: "context-compaction-v1",
	})
	if err != nil {
		t.Fatalf("create compaction: %v", err)
	}
	second, err := NewFileStore(path)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	latest, ok, err := second.GetLatestContextCompaction(conversation.ID)
	if err != nil || !ok || latest.ID != created.ID || len(latest.SourceMessageIDs) != 2 {
		t.Fatalf("compaction did not round trip: ok=%v err=%v item=%#v", ok, err, latest)
	}
}

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
      "tools": ["get_current_time"],
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

func TestFileStoreCreateAgent(t *testing.T) {
	store, err := NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	agent, err := store.CreateAgent(domain.Agent{
		Name:         "QA Agent",
		Description:  "Checks answers.",
		SystemPrompt: "Be strict.",
		Tools:        []string{"calculator", "calculator", " get_current_time "},
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
	if !agent.MemoryEnabled || !agent.RetrievalEnabled || agent.Executor != domain.DefaultAgentExecutor {
		t.Fatalf("expected default runtime config, got %#v", agent)
	}
}

func TestFileStoreArchiveAgent(t *testing.T) {
	store, err := NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	agent, err := store.CreateAgent(domain.Agent{
		Name:         "Disposable Agent",
		Description:  "Temporary.",
		SystemPrompt: "Answer once.",
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	if err := store.ArchiveAgent(agent.ID); err != nil {
		t.Fatalf("archive agent: %v", err)
	}

	agents, err := store.ListAgents()
	if err != nil {
		t.Fatalf("list agents: %v", err)
	}
	for _, item := range agents {
		if item.ID == agent.ID {
			t.Fatalf("expected archived agent to be hidden from list, got %#v", item)
		}
	}

	archived, ok, err := store.GetAgent(agent.ID)
	if err != nil {
		t.Fatalf("get archived agent: %v", err)
	}
	if !ok || !archived.Archived {
		t.Fatalf("expected archived agent to remain persisted with archived flag, got ok=%v agent=%#v", ok, archived)
	}

	if err := store.ArchiveAgent("agent_planner"); err == nil {
		t.Fatal("expected default agent archive to fail")
	}
}
