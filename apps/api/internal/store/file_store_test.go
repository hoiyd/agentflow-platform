package store

import (
	"os"
	"strings"
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

	events, err := store.ListTraceEvents(run.ID)
	if err != nil {
		t.Fatalf("list trace events after delete: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("expected no trace events after delete, got %d", len(events))
	}
}

func TestFileStoreTraceReplay(t *testing.T) {
	store, err := NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	conversation, err := store.CreateConversation("Trace test")
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
	run, err = store.UpdateRunStatus(run.ID, domain.RunRunning, "")
	if err != nil {
		t.Fatalf("mark running: %v", err)
	}
	step, err := store.CreateCollaborationStep(domain.CollaborationStep{
		RunID:          run.ID,
		ConversationID: conversation.ID,
		Role:           "planner",
		Status:         domain.CollaborationStepCompleted,
		Input:          "input",
		Output:         "output",
	})
	if err != nil {
		t.Fatalf("create step: %v", err)
	}
	if _, err := store.CreateTraceEvent(domain.TraceEvent{
		RunID:  run.ID,
		StepID: step.ID,
		Type:   domain.TraceLLMEnd,
		Payload: map[string]any{
			"prompt_tokens":         10,
			"completion_tokens":     5,
			"total_tokens":          15,
			"token_usage_estimated": true,
		},
		DurationMS: 25,
	}); err != nil {
		t.Fatalf("create llm trace: %v", err)
	}
	if _, err := store.CreateTraceEvent(domain.TraceEvent{
		RunID:      run.ID,
		StepID:     step.ID,
		Type:       domain.TraceToolEnd,
		Payload:    map[string]any{"tool_name": "calculator"},
		DurationMS: 3,
	}); err != nil {
		t.Fatalf("create tool trace: %v", err)
	}
	if _, err := store.CreateTraceEvent(domain.TraceEvent{
		RunID:   run.ID,
		StepID:  step.ID,
		Type:    domain.TraceError,
		Payload: map[string]any{"error": "boom"},
	}); err != nil {
		t.Fatalf("create error trace: %v", err)
	}

	summary, err := store.GetRunTraceSummary(run.ID)
	if err != nil {
		t.Fatalf("trace summary: %v", err)
	}
	if summary.TotalTokens != 15 || summary.PromptTokens != 10 || summary.CompletionTokens != 5 {
		t.Fatalf("unexpected token summary: %#v", summary)
	}
	if !summary.TokenUsageEstimated {
		t.Fatalf("expected estimated token summary: %#v", summary)
	}
	if summary.LLMCalls != 1 || summary.ToolCalls != 1 || summary.ErrorCount != 1 {
		t.Fatalf("unexpected call summary: %#v", summary)
	}

	replay, ok, err := store.GetRunReplay(run.ID)
	if err != nil {
		t.Fatalf("run replay: %v", err)
	}
	if !ok {
		t.Fatal("expected replay")
	}
	if replay.Run.ID != run.ID || replay.Conversation.ID != conversation.ID {
		t.Fatalf("unexpected replay identity: %#v", replay)
	}
	if len(replay.Messages) != 1 || len(replay.Steps) != 1 || len(replay.Events) != 3 {
		t.Fatalf("unexpected replay counts: messages=%d steps=%d events=%d", len(replay.Messages), len(replay.Steps), len(replay.Events))
	}
}

func TestFileStoreMemorySearchUsesMetadataSimilarityAndRecency(t *testing.T) {
	store, err := NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	now := time.Now().UTC()
	if _, err := store.CreateMemory(domain.Memory{
		Kind:      "note",
		Content:   "Use pgvector for semantic memory search.",
		Metadata:  map[string]any{"topic": "database"},
		CreatedAt: now.Add(-24 * time.Hour),
	}, domain.MemoryEmbedding{Provider: "openai_compatible", Model: "text-embedding-3-small", Embedding: []float64{1, 0}}); err != nil {
		t.Fatalf("create memory: %v", err)
	}
	if _, err := store.CreateMemory(domain.Memory{
		Kind:      "note",
		Content:   "Unrelated frontend styling note.",
		Metadata:  map[string]any{"topic": "frontend"},
		CreatedAt: now,
	}, domain.MemoryEmbedding{Provider: "local", Model: "local_hash_embedding", Embedding: []float64{0, 1}}); err != nil {
		t.Fatalf("create memory: %v", err)
	}

	items, err := store.SearchMemories(domain.MemorySearch{
		Query:             "pgvector memory",
		Embedding:         []float64{1, 0},
		EmbeddingProvider: "openai_compatible",
		EmbeddingModel:    "text-embedding-3-small",
		Metadata:          map[string]string{"topic": "database"},
		Limit:             3,
	})
	if err != nil {
		t.Fatalf("search memories: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected one filtered memory, got %d", len(items))
	}
	if !strings.Contains(items[0].Memory.Content, "pgvector") {
		t.Fatalf("expected pgvector memory, got %#v", items[0])
	}
	if items[0].Similarity <= 0 || items[0].RecencyBoost <= 0 || items[0].Score <= items[0].Similarity {
		t.Fatalf("expected similarity plus recency boost, got %#v", items[0])
	}

	items, err = store.SearchMemories(domain.MemorySearch{
		Query:             "pgvector memory",
		Embedding:         []float64{1, 0},
		EmbeddingProvider: "local",
		EmbeddingModel:    "local_hash_embedding",
		Metadata:          map[string]string{"topic": "database"},
		Limit:             3,
	})
	if err != nil {
		t.Fatalf("search model mismatch memories: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected no cross-model memories, got %d", len(items))
	}
}

func TestFileStoreDocumentSearchUsesMetadataSimilarityAndRecency(t *testing.T) {
	store, err := NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	now := time.Now().UTC()
	document, err := store.CreateDocument(domain.Document{
		Title:      "Deployment Notes",
		SourceType: "text",
		Content:    "The deployment password is amber-9137.",
		Metadata:   map[string]any{"project": "agentflow"},
		CreatedAt:  now,
	}, []domain.DocumentChunk{
		{
			Content:    "The deployment password is amber-9137.",
			TokenCount: 10,
			Metadata:   map[string]any{"project": "agentflow"},
			CreatedAt:  now,
		},
	}, []domain.DocumentChunkEmbedding{
		{Embedding: []float64{1, 0}},
	})
	if err != nil {
		t.Fatalf("create document: %v", err)
	}
	if document.ChunkCount != 1 || document.EmbeddingCount != 1 {
		t.Fatalf("expected chunk and embedding counts, got %#v", document)
	}

	items, err := store.SearchDocumentChunks(domain.DocumentSearch{
		Query:     "deployment password",
		Embedding: []float64{1, 0},
		Metadata:  map[string]string{"project": "agentflow"},
		Limit:     3,
	})
	if err != nil {
		t.Fatalf("search document chunks: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected one retrieved chunk, got %d", len(items))
	}
	if !strings.Contains(items[0].Chunk.Content, "amber-9137") {
		t.Fatalf("expected deployment chunk, got %#v", items[0])
	}
	if items[0].Similarity <= 0 || items[0].RecencyBoost <= 0 || items[0].Score <= items[0].Similarity {
		t.Fatalf("expected similarity plus recency boost, got %#v", items[0])
	}

	items, err = store.SearchDocumentChunks(domain.DocumentSearch{
		Query:             "deployment password",
		Embedding:         []float64{1, 0},
		EmbeddingProvider: "openai_compatible",
		EmbeddingModel:    "text-embedding-3-large",
		Metadata:          map[string]string{"project": "agentflow"},
		Limit:             3,
	})
	if err != nil {
		t.Fatalf("search document chunks with embedding model filter: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected embedding model mismatch to filter chunks, got %d", len(items))
	}

	items, err = store.SearchDocumentChunks(domain.DocumentSearch{
		Query:         "deployment password",
		Embedding:     []float64{0, 1},
		Metadata:      map[string]string{"project": "agentflow"},
		Limit:         3,
		MinSimilarity: 0.9,
	})
	if err != nil {
		t.Fatalf("search document chunks with threshold: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected high threshold to filter mismatched chunks, got %d", len(items))
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
