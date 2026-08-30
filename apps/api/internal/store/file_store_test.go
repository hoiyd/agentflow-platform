package store

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
)

func TestFileStoreMigratesLegacyDefaultWorkspace(t *testing.T) {
	path := t.TempDir() + "/agentflow.json"
	legacy := fileData{
		Conversations: []domain.Conversation{{ID: "conv-legacy", WorkspaceID: "default", Title: "Legacy"}},
		Messages:      []domain.Message{{ID: "msg-legacy", WorkspaceID: "default", ConversationID: "conv-legacy", Role: "user", Content: "hello"}},
		Runs:          []domain.Run{{ID: "run-legacy", WorkspaceID: "default", ConversationID: "conv-legacy"}},
		Memories:      []domain.Memory{{ID: "mem-legacy", WorkspaceID: "default", ConversationID: "conv-legacy", Kind: "note", Content: "memory"}},
		Documents:     []domain.Document{{ID: "doc-legacy", WorkspaceID: "default", Title: "Legacy document"}},
	}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatalf("marshal legacy data: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write legacy data: %v", err)
	}

	fileStore, err := NewFileStore(path)
	if err != nil {
		t.Fatalf("open legacy store: %v", err)
	}
	conversation, ok, err := fileStore.GetConversation("conv-legacy")
	if err != nil || !ok || conversation.WorkspaceID != domain.DefaultWorkspaceID {
		t.Fatalf("conversation workspace migration failed: %#v ok=%v err=%v", conversation, ok, err)
	}
	message := fileStore.data.Messages[0]
	run := fileStore.data.Runs[0]
	memory := fileStore.data.Memories[0]
	document := fileStore.data.Documents[0]
	for name, workspaceID := range map[string]string{
		"message": message.WorkspaceID, "run": run.WorkspaceID, "memory": memory.WorkspaceID, "document": document.WorkspaceID,
	} {
		if workspaceID != domain.DefaultWorkspaceID {
			t.Fatalf("%s workspace migration: got %q want %q", name, workspaceID, domain.DefaultWorkspaceID)
		}
	}
}

func TestFileStoreListsConversationEventsThroughRunOwnership(t *testing.T) {
	fileStore, err := NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	conversation, _ := fileStore.CreateConversation("Event history")
	run, err := fileStore.CreateRunWithContract("agent_planner", conversation.ID, testRuntimeSnapshot(), nil)
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

func TestFileStoreOrdersConversationEventsAndCountsHistoryFailures(t *testing.T) {
	fileStore, err := NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	conversation, _ := fileStore.CreateConversation("Ordered event history")
	otherConversation, _ := fileStore.CreateConversation("Other event history")
	firstRun, _ := fileStore.CreateRunWithContract("agent_planner", conversation.ID, testRuntimeSnapshot(), nil)
	secondRun, _ := fileStore.CreateRunWithContract("agent_planner", conversation.ID, testRuntimeSnapshot(), nil)
	otherRun, _ := fileStore.CreateRunWithContract("agent_planner", otherConversation.ID, testRuntimeSnapshot(), nil)
	base := time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC)
	created := make([]domain.RunEvent, 0, 5)
	for _, event := range []domain.RunEvent{
		{RunID: firstRun.ID, Type: domain.EventHistorySearchFailed, Timestamp: base},
		{RunID: firstRun.ID, Type: domain.EventToolCompleted, Timestamp: base},
		{RunID: secondRun.ID, Type: domain.EventToolCompleted, Timestamp: base},
		{RunID: firstRun.ID, Type: domain.EventModelFailed, Timestamp: base.Add(time.Minute)},
		{RunID: otherRun.ID, ConversationID: conversation.ID, Type: domain.EventToolCompleted, Timestamp: base},
	} {
		item, createErr := fileStore.CreateRunEvent(event)
		if createErr != nil {
			t.Fatalf("create event: %v", createErr)
		}
		created = append(created, item)
	}

	events, err := fileStore.ListConversationRunEvents(conversation.ID)
	if err != nil || len(events) != len(created) {
		t.Fatalf("conversation events: events=%#v err=%v", events, err)
	}
	for index := 1; index < len(events); index++ {
		previous, current := events[index-1], events[index]
		if current.Timestamp.Before(previous.Timestamp) ||
			(current.Timestamp.Equal(previous.Timestamp) && current.RunID < previous.RunID) ||
			(current.Timestamp.Equal(previous.Timestamp) && current.RunID == previous.RunID && current.Sequence < previous.Sequence) {
			t.Fatalf("events are not ordered: %#v", events)
		}
	}
	replay, ok, err := fileStore.GetRunReplay(firstRun.ID)
	if err != nil || !ok || replay.Summary.ErrorCount != 2 {
		t.Fatalf("history search failure was not counted: replay=%#v ok=%v err=%v", replay, ok, err)
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
	run, _ := first.CreateRunWithContract("agent_planner", conversation.ID, testRuntimeSnapshot(), nil)
	compactionID := "cmp-round-trip"
	started, err := first.CreateRunEvent(domain.RunEvent{
		Type: domain.EventCompactionStarted, RunID: run.ID, ConversationID: conversation.ID,
		Payload: map[string]any{"compaction_id": compactionID, "trigger": "soft", "status": "running", "algorithm_version": "context-compaction-v2"},
	})
	if err != nil {
		t.Fatalf("create compaction start: %v", err)
	}
	created, completed, err := first.CommitContextCompaction(domain.ContextCompaction{
		ID: compactionID, ConversationID: conversation.ID, RunID: run.ID, Trigger: "soft", Summary: "structured summary",
		SourceMessageIDs: []string{"msg-1", "msg-2"}, SourceEventIDs: []string{started.ID},
		ShadowedMessageRange: domain.ContextShadowedRange{FirstMessageID: "msg-1", LastMessageID: "msg-2", MessageCount: 2},
		SourceHash:           "hash-1", BeforeTokens: 100, AfterTokens: 25, TargetSummaryTokens: 20,
		ReductionRatio: 0.75, SummaryModel: "test", AlgorithmVersion: "context-compaction-v2",
	}, domain.RunEvent{
		Type: domain.EventCompactionCompleted, RunID: run.ID, ConversationID: conversation.ID,
		Payload: map[string]any{"compaction_id": compactionID, "trigger": "soft", "status": "completed", "algorithm_version": "context-compaction-v2"},
	})
	if err != nil || completed.Sequence != started.Sequence+1 {
		t.Fatalf("commit compaction: event=%#v err=%v", completed, err)
	}
	second, err := NewFileStore(path)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	latest, ok, err := second.GetLatestContextCompaction(conversation.ID)
	if err != nil || !ok || latest.ID != created.ID || latest.Status != domain.ContextCompactionCompleted ||
		latest.Generation != 1 || latest.ReplacementSummaryID != "summary:"+created.ID ||
		len(latest.SourceMessageIDs) != 2 || len(latest.SourceEventIDs) != 1 || latest.SurfaceReplacedAt == nil {
		t.Fatalf("compaction did not round trip: ok=%v err=%v item=%#v", ok, err, latest)
	}
}

func TestFileStoreCommitsCompactionAndTerminalEventAtomically(t *testing.T) {
	fileStore, err := NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatal(err)
	}
	conversation, _ := fileStore.CreateConversation("atomic compaction")
	run, _ := fileStore.CreateRunWithContract("agent_planner", conversation.ID, testRuntimeSnapshot(), nil)
	started, err := fileStore.CreateRunEvent(domain.RunEvent{
		Type: domain.EventCompactionStarted, RunID: run.ID, ConversationID: conversation.ID,
		Payload: map[string]any{"compaction_id": "cmp_atomic", "trigger": "hard", "status": "running", "algorithm_version": "v2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	compaction := domain.ContextCompaction{
		ID: "cmp_atomic", ConversationID: conversation.ID, RunID: run.ID, Trigger: "hard",
		Summary: "summary", SourceMessageIDs: []string{"msg-1"}, SourceHash: "atomic-hash",
		BeforeTokens: 100, AfterTokens: 25, AlgorithmVersion: "v2",
	}
	terminal := domain.RunEvent{
		Type: domain.EventCompactionCompleted, RunID: run.ID, ConversationID: conversation.ID,
		Payload: map[string]any{"compaction_id": "cmp_atomic", "trigger": "hard", "status": "completed", "algorithm_version": "v2"},
	}
	if _, _, err := fileStore.CommitContextCompaction(compaction, domain.RunEvent{Type: domain.EventCompactionFailed, RunID: run.ID, ConversationID: conversation.ID}); err == nil {
		t.Fatal("mismatched terminal event should be rejected")
	}
	created, completed, err := fileStore.CommitContextCompaction(compaction, terminal)
	if err != nil || completed.Sequence != started.Sequence+1 || created.Status != domain.ContextCompactionCompleted {
		t.Fatalf("atomic commit failed: compaction=%#v event=%#v err=%v", created, completed, err)
	}
	duplicate, duplicateEvent, err := fileStore.CommitContextCompaction(compaction, terminal)
	if err != nil || duplicate.ID != created.ID || duplicateEvent.ID != "" {
		t.Fatalf("duplicate commit was not idempotent: compaction=%#v event=%#v err=%v", duplicate, duplicateEvent, err)
	}
}

func TestFileStoreContextCompactionFailureAndLegacyPaths(t *testing.T) {
	fileStore, err := NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatal(err)
	}
	conversation, _ := fileStore.CreateConversation("compaction failures")
	run, _ := fileStore.CreateRunWithContract("agent_planner", conversation.ID, testRuntimeSnapshot(), nil)
	compaction := domain.ContextCompaction{
		ID: "cmp-failure", ConversationID: conversation.ID, RunID: run.ID, Trigger: "hard",
		Summary: "summary", SourceMessageIDs: []string{"m1", "m2"}, SourceHash: "failure-hash",
		BeforeTokens: 10, AfterTokens: 2, AlgorithmVersion: "v2",
	}
	terminal := domain.RunEvent{
		Type: domain.EventCompactionCompleted, RunID: run.ID, ConversationID: conversation.ID,
		Payload: map[string]any{"compaction_id": compaction.ID, "trigger": "hard", "status": "completed", "algorithm_version": "v2"},
	}
	missingConversation := compaction
	missingConversation.ConversationID = "missing"
	if _, _, err := fileStore.CommitContextCompaction(missingConversation, terminal); err == nil {
		t.Fatal("missing conversation should fail")
	}
	missingRun := terminal
	missingRun.RunID = "missing"
	if _, _, err := fileStore.CommitContextCompaction(compaction, missingRun); err == nil {
		t.Fatal("missing run should fail")
	}
	if _, ok, err := fileStore.GetLatestContextCompaction("missing"); err != nil || ok {
		t.Fatalf("missing latest compaction: ok=%v err=%v", ok, err)
	}
	legacy := cloneContextCompaction(domain.ContextCompaction{ID: "cmp-legacy", SourceMessageIDs: []string{"m1", "m2"}})
	if legacy.Status != domain.ContextCompactionCompleted || legacy.Generation != 1 || legacy.ReplacementSummaryID != "summary:cmp-legacy" || legacy.ShadowedMessageRange.MessageCount != 2 {
		t.Fatalf("legacy compaction was not normalized: %#v", legacy)
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

func TestFileStoreWorkspaceIsolationAcrossConversationRunAndDocument(t *testing.T) {
	fileStore, err := NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	conversationA, err := fileStore.CreateConversationInWorkspace("workspace-a", "Workspace A")
	if err != nil {
		t.Fatalf("create conversation A: %v", err)
	}
	if _, err := fileStore.CreateConversationInWorkspace("workspace-b", "Workspace B"); err != nil {
		t.Fatalf("create conversation B: %v", err)
	}
	if _, err := fileStore.AddMessageInWorkspace("workspace-a", conversationA.ID, "user", "private A"); err != nil {
		t.Fatalf("add message A: %v", err)
	}
	if _, err := fileStore.AddMessageInWorkspace("workspace-b", conversationA.ID, "user", "cross workspace"); !IsNotFound(err) {
		t.Fatalf("expected cross-workspace message write to be hidden, got %v", err)
	}
	run, err := fileStore.CreateRunWithContract("agent_planner", conversationA.ID, testRuntimeSnapshot(), nil)
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	if run.WorkspaceID != "workspace-a" {
		t.Fatalf("expected run to inherit workspace-a, got %q", run.WorkspaceID)
	}
	if _, found, err := fileStore.GetRunInWorkspace("workspace-b", run.ID); err != nil || found {
		t.Fatalf("expected run to be invisible from workspace-b, found=%v err=%v", found, err)
	}

	createDocument := func(workspaceID, title string) {
		t.Helper()
		_, createErr := fileStore.CreateDocument(domain.Document{
			WorkspaceID: workspaceID, Title: title, SourceType: "text", Content: title,
		}, []domain.DocumentChunk{{Content: title}}, []domain.DocumentChunkEmbedding{{
			Provider: "test", Model: "test", Dimensions: 2, Embedding: []float64{1, 0},
		}})
		if createErr != nil {
			t.Fatalf("create document: %v", createErr)
		}
	}
	createDocument("workspace-a", "private alpha term")
	createDocument("workspace-b", "private beta term")
	createDocument("", "private default term")
	results, err := fileStore.SearchDocumentChunksLexical(domain.DocumentSearch{
		WorkspaceID: "workspace-a", Query: "private", LexicalTerms: []string{"private"}, Limit: 10,
	})
	if err != nil {
		t.Fatalf("lexical search: %v", err)
	}
	if len(results) != 1 || results[0].Document.WorkspaceID != "workspace-a" {
		t.Fatalf("expected only workspace-a document, got %#v", results)
	}
	defaultResults, err := fileStore.SearchDocumentChunksLexical(domain.DocumentSearch{
		Query: "private", LexicalTerms: []string{"private"}, Limit: 10,
	})
	if err != nil {
		t.Fatalf("default lexical search: %v", err)
	}
	if len(defaultResults) != 1 || defaultResults[0].Document.WorkspaceID != domain.DefaultWorkspaceID {
		t.Fatalf("expected empty scope to search only the reserved workspace, got %#v", defaultResults)
	}
	conversations, err := fileStore.ListConversationsByWorkspace("workspace-a")
	if err != nil || len(conversations) != 1 || conversations[0].ID != conversationA.ID {
		t.Fatalf("unexpected workspace-a conversations: %#v err=%v", conversations, err)
	}
}
