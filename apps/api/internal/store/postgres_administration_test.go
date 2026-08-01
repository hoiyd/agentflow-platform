package store

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
)

func TestPostgresStoreAdministrativeLifecycle(t *testing.T) {
	postgresStore := openPostgresTestStore(t)
	suffix := time.Now().UTC().UnixNano()

	conversation, err := postgresStore.CreateConversation(" Initial title ")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	t.Cleanup(func() { _ = postgresStore.DeleteConversation(conversation.ID) })
	loaded, found, err := postgresStore.GetConversation(conversation.ID)
	if err != nil || !found || loaded.Title != "Initial title" {
		t.Fatalf("get conversation: found=%v item=%#v err=%v", found, loaded, err)
	}
	if err := postgresStore.UpdateConversationTitle(conversation.ID, " Updated title "); err != nil {
		t.Fatalf("update conversation title: %v", err)
	}
	conversations, err := postgresStore.ListConversations()
	if err != nil || !containsConversation(conversations, conversation.ID, "Updated title") {
		t.Fatalf("list conversations: items=%#v err=%v", conversations, err)
	}

	citations := []domain.RAGCitation{{SourceID: "S1", DocumentID: "doc-1", DocumentTitle: "Runbook", ChunkID: "chunk-1"}}
	if _, err := postgresStore.AddMessage(conversation.ID, "user", "hello"); err != nil {
		t.Fatalf("add user message: %v", err)
	}
	if _, err := postgresStore.AddMessageWithCitations(conversation.ID, "assistant", "answer [S1]", citations); err != nil {
		t.Fatalf("add assistant message: %v", err)
	}
	messages, err := postgresStore.ListMessages(conversation.ID)
	if err != nil || len(messages) != 2 || len(messages[1].Citations) != 1 || messages[1].Citations[0].SourceID != "S1" {
		t.Fatalf("list messages: items=%#v err=%v", messages, err)
	}

	agent, err := postgresStore.CreateAgent(domain.Agent{
		ID: "agent_coverage_" + fmt.Sprint(suffix), Name: " Coverage Agent ",
		Description: "Initial", SystemPrompt: "Be precise.",
		Tools: []string{"calculator", " calculator ", "get_current_time"},
	})
	if err != nil || len(agent.Tools) != 2 || !agent.MemoryEnabled || !agent.RetrievalEnabled {
		t.Fatalf("create agent: agent=%#v err=%v", agent, err)
	}
	agent.Description = "Updated"
	agent.SystemPrompt = "Updated prompt"
	updated, err := postgresStore.UpdateAgent(agent)
	if err != nil || updated.Description != "Updated" {
		t.Fatalf("update agent: agent=%#v err=%v", updated, err)
	}
	loadedAgent, found, err := postgresStore.GetAgent(agent.ID)
	if err != nil || !found || loadedAgent.SystemPrompt != "Updated prompt" {
		t.Fatalf("get agent: found=%v agent=%#v err=%v", found, loadedAgent, err)
	}
	agents, err := postgresStore.ListAgents()
	if err != nil || !containsAgent(agents, agent.ID) {
		t.Fatalf("list agents: items=%#v err=%v", agents, err)
	}
	if defaultAgent, found, err := postgresStore.GetDefaultAgent(); err != nil || !found || defaultAgent.ID == "" {
		t.Fatalf("get default agent: found=%v agent=%#v err=%v", found, defaultAgent, err)
	}
	if err := postgresStore.ArchiveAgent(agent.ID); err != nil {
		t.Fatalf("archive agent: %v", err)
	}
	archived, found, err := postgresStore.GetAgent(agent.ID)
	if err != nil || !found || !archived.Archived {
		t.Fatalf("get archived agent: found=%v agent=%#v err=%v", found, archived, err)
	}
	if err := postgresStore.ArchiveAgent("agent_planner"); err == nil {
		t.Fatal("expected default agent archive rejection")
	}

	vector := make([]float64, 1536)
	vector[0] = 1
	memoryID := "mem_coverage_" + fmt.Sprint(suffix)
	memory, err := postgresStore.CreateMemory(domain.Memory{
		ID: memoryID, WorkspaceID: "workspace-coverage", UserID: "user-coverage",
		ProjectID: "project-coverage", ConversationID: conversation.ID,
		Kind: "fact", Content: "Postgres stores durable semantic memory.",
		Metadata: map[string]any{"topic": "database"},
	}, domain.MemoryEmbedding{Provider: "test", Model: "embedding-1536", Embedding: vector})
	if err != nil || memory.ID != memoryID {
		t.Fatalf("create memory: memory=%#v err=%v", memory, err)
	}
	t.Cleanup(func() { _, _ = postgresStore.db.Exec(`DELETE FROM memories WHERE id=$1`, memoryID) })
	memories, err := postgresStore.SearchMemories(domain.MemorySearch{
		Embedding: vector, EmbeddingProvider: "test", EmbeddingModel: "embedding-1536",
		WorkspaceID: "workspace-coverage", UserID: "user-coverage", ProjectID: "project-coverage",
		Metadata: map[string]string{"topic": "database"}, Limit: 50,
	})
	if err != nil || len(memories) != 1 || memories[0].Memory.ID != memoryID || memories[0].Similarity <= 0 {
		t.Fatalf("search memories: items=%#v err=%v", memories, err)
	}
	if _, err := postgresStore.SearchMemories(domain.MemorySearch{Embedding: []float64{1}}); err == nil {
		t.Fatal("expected invalid search embedding rejection")
	}
	if _, err := postgresStore.CreateMemory(domain.Memory{Kind: "fact", Content: "invalid"}, domain.MemoryEmbedding{Embedding: []float64{1}}); err == nil {
		t.Fatal("expected invalid memory embedding rejection")
	}

	candidate := domain.MemoryCandidate{
		ID: "memcand_coverage_" + fmt.Sprint(suffix), ConversationID: conversation.ID,
		SourceMessageID: messages[0].ID, SourceRole: "user", Kind: "fact", Content: "durable fact",
		Status: domain.MemoryCandidateAccepted, ExtractionReason: "explicit", PolicyReason: "accepted", Confidence: 0.95,
	}
	if _, created, err := postgresStore.CreateMemoryCandidate(candidate); err != nil || !created {
		t.Fatalf("create memory candidate: created=%v err=%v", created, err)
	}
	allCandidates, err := postgresStore.ListMemoryCandidates("")
	if err != nil || !containsMemoryCandidate(allCandidates, candidate.ID) {
		t.Fatalf("list all memory candidates: items=%#v err=%v", allCandidates, err)
	}

	run, err := postgresStore.CreateRun("agent_planner", conversation.ID, testRuntimeSnapshot())
	if err != nil {
		t.Fatalf("create compaction run: %v", err)
	}
	compaction := domain.ContextCompaction{
		ConversationID: conversation.ID, RunID: run.ID, Trigger: "soft", Summary: "summary",
		SourceMessageIDs: []string{messages[0].ID}, SourceHash: "hash-" + fmt.Sprint(suffix),
		BeforeTokens: 100, AfterTokens: 20, SummaryModel: "local", AlgorithmVersion: "v1",
	}
	first, err := postgresStore.CreateContextCompaction(compaction)
	if err != nil {
		t.Fatalf("create compaction: %v", err)
	}
	duplicate, err := postgresStore.CreateContextCompaction(compaction)
	if err != nil || duplicate.ID != first.ID {
		t.Fatalf("idempotent compaction: first=%#v duplicate=%#v err=%v", first, duplicate, err)
	}

	run, err = postgresStore.UpdateRunAgent(run.ID, "agent_data")
	if err != nil || run.AgentID != "agent_data" {
		t.Fatalf("update run agent: run=%#v err=%v", run, err)
	}
	run, err = postgresStore.UpdateRunStatus(run.ID, domain.RunRunning, "")
	if err != nil || run.Status != domain.RunRunning || run.StartedAt == nil || run.HeartbeatAt == nil {
		t.Fatalf("start run: run=%#v err=%v", run, err)
	}
	run, err = postgresStore.UpdateRunHeartbeat(run.ID)
	if err != nil || run.HeartbeatAt == nil {
		t.Fatalf("update run heartbeat: run=%#v err=%v", run, err)
	}
	staleRuns, err := postgresStore.ListStaleRunningRuns(time.Now().UTC().Add(time.Minute))
	if err != nil || !containsRun(staleRuns, run.ID) {
		t.Fatalf("list stale runs: runs=%#v err=%v", staleRuns, err)
	}
	runs, err := postgresStore.ListRuns()
	if err != nil || !containsRun(runs, run.ID) {
		t.Fatalf("list runs: runs=%#v err=%v", runs, err)
	}
	run, err = postgresStore.UpdateRunVerificationStatus(run.ID, domain.VerificationPassed)
	if err != nil || run.VerificationStatus != domain.VerificationPassed {
		t.Fatalf("update verification status: run=%#v err=%v", run, err)
	}

	step, err := postgresStore.CreateCollaborationStep(domain.CollaborationStep{
		RunID: run.ID, ConversationID: conversation.ID, AgentID: "agent_data",
		Role: "worker", Iteration: -1, Input: " inspect data ",
	})
	if err != nil || step.Status != domain.CollaborationStepQueued || step.Iteration != 0 || step.Input != "inspect data" {
		t.Fatalf("create collaboration step: step=%#v err=%v", step, err)
	}
	step, err = postgresStore.UpdateCollaborationStepOutput(step.ID, " draft output ")
	if err != nil || step.Output != "draft output" {
		t.Fatalf("update collaboration output: step=%#v err=%v", step, err)
	}
	step, err = postgresStore.UpdateCollaborationStep(step.ID, domain.CollaborationStepCompleted, " final output ", "")
	if err != nil || step.Status != domain.CollaborationStepCompleted || step.Output != "final output" {
		t.Fatalf("complete collaboration step: step=%#v err=%v", step, err)
	}
	steps, err := postgresStore.ListCollaborationSteps(run.ID)
	if err != nil || len(steps) != 1 || steps[0].ID != step.ID {
		t.Fatalf("list collaboration steps: steps=%#v err=%v", steps, err)
	}

	modelEvent, err := postgresStore.CreateRunEvent(domain.RunEvent{
		RunID: run.ID, ConversationID: conversation.ID, StageID: step.ID,
		Type: domain.EventModelCompleted,
		Payload: map[string]any{
			"prompt_tokens": 11, "completion_tokens": 7, "total_tokens": 18,
			"token_usage_estimated": true,
		},
	})
	if err != nil || modelEvent.Sequence != 1 || modelEvent.SchemaVersion != domain.CurrentRunEventSchemaVersion {
		t.Fatalf("create model event: event=%#v err=%v", modelEvent, err)
	}
	toolEvent, err := postgresStore.CreateRunEvent(domain.RunEvent{
		RunID: run.ID, StageID: step.ID, ParentEventID: modelEvent.ID,
		Type: domain.EventToolFailed, Payload: map[string]any{"tool_name": "coverage"},
	})
	if err != nil || toolEvent.Sequence != 2 {
		t.Fatalf("create tool event: event=%#v err=%v", toolEvent, err)
	}
	events, err := postgresStore.ListRunEvents(run.ID)
	if err != nil || len(events) != 2 || events[0].ID != modelEvent.ID || events[1].ParentEventID != modelEvent.ID {
		t.Fatalf("list run events: events=%#v err=%v", events, err)
	}
	summary, err := postgresStore.GetRunTraceSummary(run.ID)
	if err != nil || summary.LLMCalls != 1 || summary.ToolCalls != 1 || summary.ErrorCount != 0 || summary.TotalTokens != 18 || !summary.TokenUsageEstimated {
		t.Fatalf("get trace summary: summary=%#v err=%v", summary, err)
	}
	replay, found, err := postgresStore.GetRunReplay(run.ID)
	if err != nil || !found || len(replay.Steps) != 1 || len(replay.RunEvents) != 2 {
		t.Fatalf("get run replay: found=%v replay=%#v err=%v", found, replay, err)
	}
	run, err = postgresStore.UpdateRunStatus(run.ID, domain.RunCompleted, "")
	if err != nil || run.Status != domain.RunCompleted || run.CompletedAt == nil {
		t.Fatalf("complete run: run=%#v err=%v", run, err)
	}

	if _, found, err := postgresStore.GetConversation("missing"); err != nil || found {
		t.Fatalf("expected missing conversation: found=%v err=%v", found, err)
	}
	if err := postgresStore.UpdateConversationTitle("missing", "title"); !IsNotFound(err) {
		t.Fatalf("expected typed missing title error, got %v", err)
	}
	if err := postgresStore.DeleteConversation("missing"); !IsNotFound(err) {
		t.Fatalf("expected typed missing delete error, got %v", err)
	}
	if _, err := postgresStore.CreateAgent(domain.Agent{Name: "   "}); err == nil {
		t.Fatal("expected blank agent name rejection")
	}
	if _, err := postgresStore.UpdateAgent(domain.Agent{ID: "missing", Name: "missing"}); err == nil {
		t.Fatal("expected missing agent update rejection")
	}
	if err := postgresStore.ArchiveAgent("missing"); err == nil {
		t.Fatal("expected missing agent archive rejection")
	}
	if _, err := postgresStore.UpdateRunHeartbeat("missing"); err == nil {
		t.Fatal("expected missing run heartbeat rejection")
	}
	if _, err := postgresStore.UpdateRunAgent(run.ID, "missing"); err == nil {
		t.Fatal("expected missing run agent rejection")
	}
	if _, err := postgresStore.UpdateCollaborationStep("missing", domain.CollaborationStepFailed, "", "failed"); err == nil {
		t.Fatal("expected missing collaboration step rejection")
	}
	if _, err := postgresStore.UpdateCollaborationStepOutput("missing", "output"); err == nil {
		t.Fatal("expected missing collaboration output rejection")
	}
	if _, err := postgresStore.CreateCollaborationStep(domain.CollaborationStep{RunID: run.ID}); err == nil {
		t.Fatal("expected blank collaboration role rejection")
	}
	if _, err := postgresStore.CreateRunEvent(domain.RunEvent{RunID: run.ID}); err == nil {
		t.Fatal("expected blank run event type rejection")
	}
	if _, err := postgresStore.GetRunTraceSummary("missing"); err == nil {
		t.Fatalf("expected missing trace summary rejection, got %v", err)
	}
}

func openPostgresTestStore(t *testing.T) *PostgresStore {
	t.Helper()
	databaseURL := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	postgresStore, err := NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatalf("new postgres store: %v", err)
	}
	t.Cleanup(func() { _ = postgresStore.Close() })
	return postgresStore
}

func containsConversation(items []domain.Conversation, id string, title string) bool {
	for _, item := range items {
		if item.ID == id && item.Title == title {
			return true
		}
	}
	return false
}

func containsAgent(items []domain.Agent, id string) bool {
	for _, item := range items {
		if item.ID == id {
			return true
		}
	}
	return false
}

func containsMemoryCandidate(items []domain.MemoryCandidate, id string) bool {
	for _, item := range items {
		if item.ID == id {
			return true
		}
	}
	return false
}

func containsRun(items []domain.Run, id string) bool {
	for _, item := range items {
		if item.ID == id {
			return true
		}
	}
	return false
}
