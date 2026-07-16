package contextcompaction

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"agentflow-platform/apps/api/internal/contextassembly"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/store"
)

func TestCompactIfNeededPersistsNonDestructiveIterativeSummary(t *testing.T) {
	fileStore, conversation, run := newCompactionTestStore(t)
	for index := 0; index < 8; index++ {
		role := "user"
		if index%2 == 1 {
			role = "assistant"
		}
		if _, err := fileStore.AddMessage(conversation.ID, role, strings.Repeat("important context ", 8)); err != nil {
			t.Fatalf("add message: %v", err)
		}
	}

	service := NewService(fileStore)
	var prompts []string
	summarizer := SummarizerFunc(func(_ context.Context, request SummaryRequest) (SummaryResult, error) {
		prompts = append(prompts, request.Prompt)
		return SummaryResult{Text: "## Goal\nPreserve important context.\n## Source References\nmessage ids retained", Model: "summary-model"}, nil
	})
	config := compactionTestConfig()
	first, created, err := service.CompactIfNeeded(context.Background(), Request{
		RunID: run.ID, ConversationID: conversation.ID, Trigger: contextassembly.CompactionTriggerSoft,
		Config: config, Summarizer: summarizer,
	})
	if err != nil || !created {
		t.Fatalf("first compaction: created=%v err=%v", created, err)
	}
	if len(first.SourceMessageIDs) == 0 || first.BeforeTokens <= first.AfterTokens {
		t.Fatalf("unexpected first compaction: %#v", first)
	}
	messages, _ := fileStore.ListMessages(conversation.ID)
	if len(messages) != 8 {
		t.Fatalf("compaction modified raw messages: got %d", len(messages))
	}

	for index := 0; index < 4; index++ {
		role := "user"
		if index%2 == 1 {
			role = "assistant"
		}
		_, _ = fileStore.AddMessage(conversation.ID, role, strings.Repeat("new context ", 10))
	}
	second, created, err := service.CompactIfNeeded(context.Background(), Request{
		RunID: run.ID, ConversationID: conversation.ID, Trigger: contextassembly.CompactionTriggerHard,
		Config: config, Summarizer: summarizer,
	})
	if err != nil || !created {
		t.Fatalf("second compaction: created=%v err=%v", created, err)
	}
	if len(second.SourceMessageIDs) <= len(first.SourceMessageIDs) {
		t.Fatalf("iterative compaction did not extend sources: first=%d second=%d", len(first.SourceMessageIDs), len(second.SourceMessageIDs))
	}
	if len(prompts) != 2 || !strings.Contains(prompts[1], "PREVIOUS COMPACTION SUMMARY") {
		t.Fatalf("previous summary was not carried forward: %#v", prompts)
	}
	events, _ := fileStore.ListRunEvents(run.ID)
	if !hasEvent(events, domain.EventCompactionStarted) || !hasEvent(events, domain.EventCompactionCompleted) {
		t.Fatalf("missing compaction lifecycle events: %#v", events)
	}
}

func TestCompactIfNeededFailureKeepsRawMessages(t *testing.T) {
	fileStore, conversation, run := newCompactionTestStore(t)
	for index := 0; index < 6; index++ {
		_, _ = fileStore.AddMessage(conversation.ID, "user", strings.Repeat("context ", 20))
	}
	service := NewService(fileStore)
	_, created, err := service.CompactIfNeeded(context.Background(), Request{
		RunID: run.ID, ConversationID: conversation.ID, Trigger: contextassembly.CompactionTriggerHard,
		Config: compactionTestConfig(), Summarizer: SummarizerFunc(func(context.Context, SummaryRequest) (SummaryResult, error) {
			return SummaryResult{}, errors.New("summary provider unavailable")
		}),
	})
	if err == nil || created {
		t.Fatalf("expected non-destructive failure: created=%v err=%v", created, err)
	}
	compactions, _ := fileStore.ListContextCompactions(conversation.ID)
	messages, _ := fileStore.ListMessages(conversation.ID)
	if len(compactions) != 0 || len(messages) != 6 {
		t.Fatalf("failed compaction changed durable state: compactions=%d messages=%d", len(compactions), len(messages))
	}
	events, _ := fileStore.ListRunEvents(run.ID)
	if !hasEvent(events, domain.EventCompactionFailed) {
		t.Fatalf("missing failed event: %#v", events)
	}
}

func TestCompactIfNeededPrefersObservedPromptTokensForSoftTrigger(t *testing.T) {
	fileStore, conversation, run := newCompactionTestStore(t)
	for index := 0; index < 6; index++ {
		_, _ = fileStore.AddMessage(conversation.ID, "user", "short context")
	}
	config := compactionTestConfig()
	config.CompactionSoftThreshold = 0.9
	service := NewService(fileStore)
	_, created, err := service.CompactIfNeeded(context.Background(), Request{
		RunID: run.ID, ConversationID: conversation.ID, Trigger: contextassembly.CompactionTriggerSoft,
		ObservedPromptTokens: 200, Config: config,
		Summarizer: SummarizerFunc(func(context.Context, SummaryRequest) (SummaryResult, error) {
			return SummaryResult{Text: "## Goal\nKeep context", Model: "summary-model"}, nil
		}),
	})
	if err != nil || !created {
		t.Fatalf("observed prompt usage should trigger compaction: created=%v err=%v", created, err)
	}
	events, _ := fileStore.ListRunEvents(run.ID)
	foundObservedUsage := false
	for _, runEvent := range events {
		if runEvent.Type == domain.EventCompactionCompleted && runEvent.Payload["observed_prompt_tokens"] == float64(200) {
			foundObservedUsage = true
		}
	}
	if !foundObservedUsage {
		t.Fatalf("completed event did not retain observed prompt tokens: %#v", events)
	}
}

func newCompactionTestStore(t *testing.T) (*store.FileStore, domain.Conversation, domain.Run) {
	t.Helper()
	fileStore, err := store.NewFileStore(filepath.Join(t.TempDir(), "agentflow.json"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	conversation, err := fileStore.CreateConversation("compaction test")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	run, err := fileStore.CreateRun("agent_planner", conversation.ID, domain.RuntimeSnapshot{
		SchemaVersion: domain.CurrentRuntimeSnapshotVersion, Mode: "single",
		Agent: domain.RuntimeAgentSnapshot{ID: "agent_planner"}, Model: domain.RuntimeModelSnapshot{Model: "test"},
		ContextAssembly: compactionTestConfig(),
	})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	return fileStore, conversation, run
}

func compactionTestConfig() domain.ContextAssemblyConfig {
	return domain.ContextAssemblyConfig{
		ContextWindowTokens: 240, OutputReserveTokens: 20, SafetyMarginTokens: 20,
		HistoryMaxTokens: 160, MemoryMaxTokens: 20, KnowledgeMaxTokens: 20,
		CompactionMode: contextassembly.CompactionModeAuto, CompactionVersion: contextassembly.CompactionVersion,
		CompactionSoftThreshold: 0.25, CompactionHardThreshold: 0.30,
		CompactionRecentTokens: 24, CompactionSummaryMaxTokens: 40,
		CompactionToolResultMaxTokens: 20, CompactionTimeoutMS: 1000,
	}
}

func hasEvent(events []domain.RunEvent, eventType domain.RunEventType) bool {
	for _, event := range events {
		if event.Type == eventType {
			return true
		}
	}
	return false
}
