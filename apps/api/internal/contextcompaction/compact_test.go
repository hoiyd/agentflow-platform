package contextcompaction

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/contextassembly"
	"agentflow-platform/apps/api/internal/domain"
	eventpkg "agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/failure"
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

	compactor := NewCompactor(fileStore)
	var prompts []string
	summarizer := SummarizerFunc(func(_ context.Context, request SummaryRequest) (SummaryResult, error) {
		prompts = append(prompts, request.Prompt)
		return SummaryResult{Text: "## Goal\nPreserve important context.\n## Source References\nmessage ids retained", Model: "summary-model"}, nil
	})
	config := compactionTestConfig()
	first, err := compactor.CompactIfNeeded(context.Background(), Request{
		RunID: run.ID, ConversationID: conversation.ID, Trigger: contextassembly.CompactionTriggerSoft,
		Config: config, Summarizer: summarizer,
	})
	if err != nil || first == nil {
		t.Fatalf("first compaction: item=%#v err=%v", first, err)
	}
	if len(first.SourceMessageIDs) == 0 || first.BeforeTokens <= first.AfterTokens {
		t.Fatalf("unexpected first compaction: %#v", first)
	}
	if first.Generation != 1 || first.Status != domain.ContextCompactionCompleted || first.ReplacementSummaryID != "summary:"+first.ID || first.SurfaceReplacedAt == nil {
		t.Fatalf("missing first generation lineage: %#v", first)
	}
	if first.TargetSummaryTokens <= 0 || first.TargetSummaryTokens > config.CompactionSummaryMaxTokens || first.ShadowedMessageRange.MessageCount != len(first.SourceMessageIDs) {
		t.Fatalf("invalid target or shadowed range: %#v", first)
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
	second, err := compactor.CompactIfNeeded(context.Background(), Request{
		RunID: run.ID, ConversationID: conversation.ID, Trigger: contextassembly.CompactionTriggerHard,
		Config: config, Summarizer: summarizer,
	})
	if err != nil || second == nil {
		t.Fatalf("second compaction: item=%#v err=%v", second, err)
	}
	if len(second.SourceMessageIDs) <= len(first.SourceMessageIDs) {
		t.Fatalf("iterative compaction did not extend sources: first=%d second=%d", len(first.SourceMessageIDs), len(second.SourceMessageIDs))
	}
	if second.Generation != 2 || second.PreviousCompactionID != first.ID {
		t.Fatalf("iterative lineage was not persisted: %#v", second)
	}
	if len(prompts) != 2 || !strings.Contains(prompts[1], "PREVIOUS COMPACTION SUMMARY") {
		t.Fatalf("previous summary was not carried forward: %#v", prompts)
	}
	if !strings.Contains(prompts[1], "newer sources override") || !strings.Contains(compactionSystemPrompt, "## Superseded Instructions") || !strings.Contains(compactionSystemPrompt, "## Evidence Needed") {
		t.Fatalf("anti-drift schema or precedence rule missing")
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
	compactor := NewCompactor(fileStore)
	calls := 0
	compaction, err := compactor.CompactIfNeeded(context.Background(), Request{
		RunID: run.ID, ConversationID: conversation.ID, Trigger: contextassembly.CompactionTriggerHard,
		Config: compactionTestConfig(), Summarizer: SummarizerFunc(func(context.Context, SummaryRequest) (SummaryResult, error) {
			calls++
			return SummaryResult{}, errors.New("summary provider unavailable")
		}),
	})
	if err == nil || compaction != nil {
		t.Fatalf("expected non-destructive failure: item=%#v err=%v", compaction, err)
	}
	_, hasCompaction, _ := fileStore.GetLatestContextCompaction(conversation.ID)
	messages, _ := fileStore.ListMessages(conversation.ID)
	if hasCompaction || len(messages) != 6 {
		t.Fatalf("failed compaction changed durable state: has_compaction=%v messages=%d", hasCompaction, len(messages))
	}
	events, _ := fileStore.ListRunEvents(run.ID)
	if !hasEvent(events, domain.EventCompactionFailed) {
		t.Fatalf("missing failed event: %#v", events)
	}
	compaction, err = compactor.CompactIfNeeded(context.Background(), Request{
		RunID: run.ID, ConversationID: conversation.ID, Trigger: contextassembly.CompactionTriggerHard,
		Config: compactionTestConfig(), Summarizer: SummarizerFunc(func(context.Context, SummaryRequest) (SummaryResult, error) {
			calls++
			return SummaryResult{Text: "should be suppressed"}, nil
		}),
	})
	if err != nil || compaction != nil || calls != 1 {
		t.Fatalf("temporary failure cooldown did not suppress immediate retry: item=%#v calls=%d err=%v", compaction, calls, err)
	}
}

func TestCompactIfNeededPrefersObservedPromptTokensForSoftTrigger(t *testing.T) {
	fileStore, conversation, run := newCompactionTestStore(t)
	for index := 0; index < 6; index++ {
		_, _ = fileStore.AddMessage(conversation.ID, "user", "short context")
	}
	config := compactionTestConfig()
	config.CompactionSoftThreshold = 0.9
	compactor := NewCompactor(fileStore)
	compaction, err := compactor.CompactIfNeeded(context.Background(), Request{
		RunID: run.ID, ConversationID: conversation.ID, Trigger: contextassembly.CompactionTriggerSoft,
		ObservedPromptTokens: 200, Config: config,
		Summarizer: SummarizerFunc(func(context.Context, SummaryRequest) (SummaryResult, error) {
			return SummaryResult{Text: "## Goal\nKeep context", Model: "summary-model"}, nil
		}),
	})
	if err != nil || compaction == nil {
		t.Fatalf("observed prompt usage should trigger compaction: item=%#v err=%v", compaction, err)
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

func TestCompactIfNeededFailurePaths(t *testing.T) {
	t.Run("missing summarizer starts then fails with configuration cooldown", func(t *testing.T) {
		base, conversation, run := populatedCompactionTestStore(t, 6)
		compactor := NewCompactor(base)
		item, err := compactor.CompactIfNeeded(context.Background(), Request{
			RunID: run.ID, ConversationID: conversation.ID, Trigger: contextassembly.CompactionTriggerHard,
			Config: compactionTestConfig(),
		})
		if !errors.Is(err, ErrSummaryUnavailable) || item != nil {
			t.Fatalf("expected unavailable summarizer: item=%#v err=%v", item, err)
		}
		events, _ := base.ListRunEvents(run.ID)
		if len(events) != 2 || events[0].Type != domain.EventCompactionStarted || events[1].Type != domain.EventCompactionFailed || events[1].Payload["cooldown_reason"] != "summarizer_configuration_failure" {
			t.Fatalf("unexpected missing-summarizer lifecycle: %#v", events)
		}
	})

	t.Run("empty summary is a failed attempt", func(t *testing.T) {
		base, conversation, run := populatedCompactionTestStore(t, 6)
		item, err := NewCompactor(base).CompactIfNeeded(context.Background(), Request{
			RunID: run.ID, ConversationID: conversation.ID, Trigger: contextassembly.CompactionTriggerHard,
			Config: compactionTestConfig(), Summarizer: SummarizerFunc(func(context.Context, SummaryRequest) (SummaryResult, error) {
				return SummaryResult{Model: "empty-model"}, nil
			}),
		})
		if !errors.Is(err, ErrSummaryUnavailable) || item != nil {
			t.Fatalf("expected empty summary failure: item=%#v err=%v", item, err)
		}
	})

	t.Run("start event failure prevents provider call", func(t *testing.T) {
		base, conversation, run := populatedCompactionTestStore(t, 6)
		fault := &compactionFaultStore{Store: base, failEventType: domain.EventCompactionStarted, eventErr: errors.New("event sink down")}
		calls := 0
		item, err := NewCompactor(fault).CompactIfNeeded(context.Background(), Request{
			RunID: run.ID, ConversationID: conversation.ID, Trigger: contextassembly.CompactionTriggerHard,
			Config: compactionTestConfig(), Summarizer: SummarizerFunc(func(context.Context, SummaryRequest) (SummaryResult, error) {
				calls++
				return SummaryResult{Text: "summary"}, nil
			}),
		})
		if err == nil || item != nil || calls != 0 {
			t.Fatalf("start failure must abort before summarization: item=%#v calls=%d err=%v", item, calls, err)
		}
	})

	t.Run("atomic commit failure cannot expose summary", func(t *testing.T) {
		base, conversation, run := populatedCompactionTestStore(t, 6)
		fault := &compactionFaultStore{Store: base, commitErr: errors.New("commit failed")}
		compactor := NewCompactor(fault)
		item, err := compactor.CompactIfNeeded(context.Background(), Request{
			RunID: run.ID, ConversationID: conversation.ID, Trigger: contextassembly.CompactionTriggerHard,
			Config: compactionTestConfig(), Summarizer: fixedSummarizer("summary"),
		})
		_, visible, _ := base.GetLatestContextCompaction(conversation.ID)
		if err == nil || item != nil || visible {
			t.Fatalf("failed commit exposed a summary: item=%#v visible=%v err=%v", item, visible, err)
		}
		if compactor.state[conversation.ID].consecutiveLowYield != 0 {
			t.Fatal("failed commit mutated low-yield policy state")
		}
	})

	t.Run("failure event error is joined with provider error", func(t *testing.T) {
		base, conversation, run := populatedCompactionTestStore(t, 6)
		fault := &compactionFaultStore{Store: base, failEventType: domain.EventCompactionFailed, eventErr: errors.New("failure event rejected")}
		_, err := NewCompactor(fault).CompactIfNeeded(context.Background(), Request{
			RunID: run.ID, ConversationID: conversation.ID, Trigger: contextassembly.CompactionTriggerHard,
			Config: compactionTestConfig(), Summarizer: SummarizerFunc(func(context.Context, SummaryRequest) (SummaryResult, error) {
				return SummaryResult{}, context.DeadlineExceeded
			}),
		})
		if err == nil || !strings.Contains(err.Error(), "failure event rejected") || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("expected joined provider and event error, got %v", err)
		}
	})
}

func TestCompactIfNeededStoreAndRequestFailurePaths(t *testing.T) {
	if _, err := (*Compactor)(nil).CompactIfNeeded(context.Background(), Request{}); err == nil {
		t.Fatal("nil compactor should fail")
	}
	base, conversation, run := populatedCompactionTestStore(t, 6)
	off := compactionTestConfig()
	off.CompactionMode = contextassembly.CompactionModeOff
	if item, err := NewCompactor(base).CompactIfNeeded(context.Background(), Request{Config: off}); err != nil || item != nil {
		t.Fatalf("disabled compaction should be a no-op: item=%#v err=%v", item, err)
	}
	messageFault := &compactionFaultStore{Store: base, messagesErr: errors.New("messages unavailable")}
	if _, err := NewCompactor(messageFault).CompactIfNeeded(context.Background(), Request{
		RunID: run.ID, ConversationID: conversation.ID, Trigger: contextassembly.CompactionTriggerHard, Config: compactionTestConfig(),
	}); err == nil || !strings.Contains(err.Error(), "messages unavailable") {
		t.Fatalf("message store failure was not returned: %v", err)
	}
	latestFault := &compactionFaultStore{Store: base, latestErr: errors.New("latest unavailable")}
	if _, err := NewCompactor(latestFault).CompactIfNeeded(context.Background(), Request{
		RunID: run.ID, ConversationID: conversation.ID, Trigger: contextassembly.CompactionTriggerHard, Config: compactionTestConfig(),
	}); err == nil || !strings.Contains(err.Error(), "latest unavailable") {
		t.Fatalf("latest store failure was not returned: %v", err)
	}
	if _, err := NewCompactor(base).CompactIfNeeded(context.Background(), Request{
		ConversationID: conversation.ID, Trigger: contextassembly.CompactionTriggerHard,
		Config: compactionTestConfig(), Summarizer: fixedSummarizer("summary"),
	}); err == nil || !strings.Contains(err.Error(), "run id is required") {
		t.Fatalf("missing run id was not rejected: %v", err)
	}
	duplicateFault := &compactionFaultStore{Store: base, duplicate: &domain.ContextCompaction{ID: "cmp-concurrent"}}
	item, err := NewCompactor(duplicateFault).CompactIfNeeded(context.Background(), Request{
		RunID: run.ID, ConversationID: conversation.ID, Trigger: contextassembly.CompactionTriggerHard,
		Config: compactionTestConfig(), Summarizer: fixedSummarizer("summary"),
	})
	if err != nil || item == nil || item.ID != "cmp-concurrent" {
		t.Fatalf("concurrent duplicate was not handled: item=%#v err=%v", item, err)
	}
	if got := truncateError(strings.Repeat("x", 1100)); !strings.HasSuffix(got, "...[truncated]") {
		t.Fatalf("long error was not truncated: %q", got)
	}
}

func TestCompactionPolicyGuardsLowYieldAndOverflow(t *testing.T) {
	compactor := NewCompactor(nil)
	now := time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC)
	compactor.now = func() time.Time { return now }
	request := Request{ConversationID: "conv", Trigger: contextassembly.CompactionTriggerHard}
	if got := compactor.recordOutcome("conv", 0.05); got != 1 {
		t.Fatalf("first low-yield count = %d", got)
	}
	if got := compactor.recordOutcome("conv", -0.20); got != 2 || compactor.allowAttempt(request, "hash") {
		t.Fatalf("second low-yield result did not open fuse: count=%d", got)
	}
	now = now.Add(lowYieldCooldown + time.Second)
	if !compactor.allowAttempt(request, "hash") || compactor.state["conv"].consecutiveLowYield != 0 || compactor.recordOutcome("conv", 0.50) != 0 {
		t.Fatal("low-yield cooldown did not expire and reset")
	}

	overflow := Request{ConversationID: "overflow", Trigger: contextassembly.CompactionTriggerOverflow, TriggerKey: "turn-1"}
	if !compactor.allowAttempt(overflow, "hash-1") {
		t.Fatal("first overflow recovery should be allowed")
	}
	compactor.markAttempt(overflow, "hash-1")
	if compactor.allowAttempt(overflow, "hash-1") {
		t.Fatal("same overflow trigger was allowed twice")
	}
	overflow.TriggerKey = "turn-2"
	if !compactor.allowAttempt(overflow, "hash-1") {
		t.Fatal("new turn should reset overflow guard")
	}
	compactor.markAttempt(Request{ConversationID: "overflow", Trigger: contextassembly.CompactionTriggerHard}, "hash-1")

	failureErr := failure.New(failure.Definition{Message: "bad config", Info: failure.Info{Code: "bad_config", Category: failure.CategoryValidation}})
	reason, until := compactor.recordFailure("config", failureErr, false)
	if reason != "summarizer_configuration_failure" || !until.Equal(now.Add(configurationCooldown)) {
		t.Fatalf("configuration cooldown mismatch: reason=%s until=%s", reason, until)
	}
	storedReason, storedUntil := compactor.currentCooldown("config")
	if storedReason != reason || storedUntil == nil || !storedUntil.Equal(until) {
		t.Fatalf("cooldown snapshot mismatch: reason=%s until=%v", storedReason, storedUntil)
	}
	compactor.seedPolicyFromCompaction(domain.ContextCompaction{ConversationID: "restart", ConsecutiveLowYield: 1})
	if compactor.recordOutcome("restart", 0.01) != 2 {
		t.Fatal("persisted low-yield count was not restored")
	}
	if reason, until := compactor.currentCooldown("missing"); reason != "" || until != nil {
		t.Fatalf("empty cooldown should not be materialized: reason=%q until=%v", reason, until)
	}
}

func TestBuildPlanKeepsProtocolGroupsIndivisible(t *testing.T) {
	messages := []domain.Message{
		{ID: "u1", Role: "user", Content: strings.Repeat("first ", 20)},
		{ID: "a1", Role: "assistant", Content: "tool call"},
		{ID: "t1", Role: "tool", Content: "tool result"},
		{ID: "a2", Role: "assistant", Content: "tool conclusion"},
		{ID: "u2", Role: "user", Content: "second"}, {ID: "a3", Role: "assistant", Content: "answer"},
		{ID: "u3", Role: "user", Content: "third"}, {ID: "a4", Role: "assistant", Content: "answer"},
	}
	config := compactionTestConfig()
	config.CompactionRecentTokens = 10
	plan := buildPlan(messages, nil, config)
	if strings.Join(plan.sourceIDs, ",") != "u1,a1,t1,a2" {
		t.Fatalf("tool protocol group was split: %#v", plan.sourceIDs)
	}
	if plan.shadowedRange.FirstMessageID != "u1" || plan.shadowedRange.LastMessageID != "a2" || plan.shadowedRange.MessageCount != 4 {
		t.Fatalf("incorrect shadowed range: %#v", plan.shadowedRange)
	}
	if targetSummaryTokens(0, 0) != 1 || targetSummaryTokens(10000, 2000) != 2000 || tokenReductionRatio(0, 10) != 0 {
		t.Fatal("dynamic budget edge cases changed")
	}
	if got := limitSummary(strings.Repeat("long summary ", 100), 20); !strings.Contains(got, "summary truncated") {
		t.Fatalf("summary hard cap was not applied: %q", got)
	}
}

func TestReconcileOrphanCompaction(t *testing.T) {
	base, conversation, run := newCompactionTestStore(t)
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	payload, err := eventpkg.Payload(eventpkg.ContextCompactionPayload{
		CompactionID: "cmp_orphan", Trigger: contextassembly.CompactionTriggerHard,
		Status: "running", AlgorithmVersion: AlgorithmVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = base.CreateRunEvent(domain.RunEvent{
		Type: domain.EventCompactionStarted, RunID: run.ID, ConversationID: conversation.ID,
		Payload: payload, Timestamp: now.Add(-2 * time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	compactor := NewCompactor(base)
	compactor.now = func() time.Time { return now }
	if err := compactor.ReconcileOrphans(conversation.ID, time.Second); err != nil {
		t.Fatalf("reconcile orphan: %v", err)
	}
	if err := compactor.ReconcileOrphans(conversation.ID, time.Second); err != nil {
		t.Fatalf("idempotent reconcile: %v", err)
	}
	events, _ := base.ListRunEvents(run.ID)
	failed := 0
	for _, runEvent := range events {
		if runEvent.Type == domain.EventCompactionFailed && runEvent.Payload["recovered_from_orphan"] == true {
			failed++
		}
	}
	if failed != 1 {
		t.Fatalf("orphan should produce exactly one recovered failure: %#v", events)
	}
	if err := (*Compactor)(nil).ReconcileOrphans(conversation.ID, time.Second); err == nil {
		t.Fatal("nil compactor should fail")
	}
	fault := &compactionFaultStore{Store: base, listErr: errors.New("event scan failed")}
	if err := NewCompactor(fault).ReconcileOrphans(conversation.ID, time.Second); err == nil {
		t.Fatal("event scan failure should be returned")
	}
}

func TestCooldownHydratesFromPersistedEventPayload(t *testing.T) {
	compactor := NewCompactor(nil)
	now := time.Date(2026, 8, 28, 14, 0, 0, 0, time.UTC)
	compactor.now = func() time.Time { return now }
	until := now.Add(10 * time.Minute)
	compactor.hydratePolicyFromEvents("conv", []domain.RunEvent{{Payload: map[string]any{
		"cooldown_reason": "summarizer_configuration_failure", "cooldown_until": until.Format(time.RFC3339Nano),
	}}, {Payload: map[string]any{
		"trigger": contextassembly.CompactionTriggerOverflow, "trigger_key": "turn-persisted",
	}}})
	if compactor.allowAttempt(Request{ConversationID: "conv"}, "hash") {
		t.Fatal("persisted cooldown was not restored")
	}
	now = until.Add(time.Second)
	if compactor.allowAttempt(Request{ConversationID: "conv", Trigger: contextassembly.CompactionTriggerOverflow, TriggerKey: "turn-persisted"}, "hash") {
		t.Fatal("persisted overflow guard was not restored")
	}
	if parsed, ok := timeValue(&until); !ok || !parsed.Equal(until) {
		t.Fatalf("time pointer payload was not parsed: parsed=%v ok=%v", parsed, ok)
	}
	if _, ok := timeValue(42); ok {
		t.Fatal("invalid cooldown timestamp should be rejected")
	}
}

func TestLongSessionSemanticFidelityFixturePreservesCorrectionsAndCancellation(t *testing.T) {
	fileStore, conversation, run := newCompactionTestStore(t)
	oldUser, _ := fileStore.AddMessage(conversation.ID, "user", "Use model A and deploy the legacy task.")
	oldAssistant, _ := fileStore.AddMessage(conversation.ID, "assistant", "Model A and deployment are active.")
	_, err := fileStore.CreateContextCompaction(domain.ContextCompaction{
		ConversationID: conversation.ID, RunID: run.ID, Trigger: "fixture",
		Generation: 1, Summary: "## Key Decisions\nUse model A.\n## Pending Work\nDeploy the legacy task.",
		SourceMessageIDs: []string{oldUser.ID, oldAssistant.ID}, SourceHash: "fixture-generation-1",
		BeforeTokens: 80, AfterTokens: 25, AlgorithmVersion: AlgorithmVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	newSources := []struct{ role, content string }{
		{"user", "Correction: use model B, not model A."}, {"assistant", "Model B supersedes model A."},
		{"user", "Cancel the legacy deployment task."}, {"assistant", "The deployment task is canceled."},
		{"user", "Keep exact reference ticket-4821."}, {"assistant", "Reference preserved."},
		{"user", "What remains?"}, {"assistant", "Review the current state."},
	}
	for _, source := range newSources {
		_, _ = fileStore.AddMessage(conversation.ID, source.role, source.content)
	}
	compaction, err := NewCompactor(fileStore).CompactIfNeeded(context.Background(), Request{
		RunID: run.ID, ConversationID: conversation.ID, Trigger: contextassembly.CompactionTriggerHard,
		ObservedPromptTokens: 1000, Config: compactionTestConfig(),
		Summarizer: SummarizerFunc(func(_ context.Context, request SummaryRequest) (SummaryResult, error) {
			if !strings.Contains(request.Prompt, "Use model A") || !strings.Contains(request.Prompt, "Correction: use model B") || !strings.Contains(request.Prompt, "Cancel the legacy deployment") {
				t.Fatalf("fixture omitted correction history: %s", request.Prompt)
			}
			return SummaryResult{Text: "## Key Decisions\nUse model B.\n## Superseded Instructions\nModel A and legacy deployment are canceled.\n## Exact References\nticket-4821", Model: "fixture-model"}, nil
		}),
	})
	if err != nil || compaction == nil || compaction.Generation != 2 ||
		!strings.Contains(compaction.Summary, "Use model B") || !strings.Contains(compaction.Summary, "legacy deployment are canceled") {
		t.Fatalf("semantic fidelity fixture failed: compaction=%#v err=%v", compaction, err)
	}
}

func fixedSummarizer(summary string) Summarizer {
	return SummarizerFunc(func(context.Context, SummaryRequest) (SummaryResult, error) {
		return SummaryResult{Text: summary, Model: "summary-model"}, nil
	})
}

func populatedCompactionTestStore(t *testing.T, count int) (*store.FileStore, domain.Conversation, domain.Run) {
	t.Helper()
	fileStore, conversation, run := newCompactionTestStore(t)
	for index := 0; index < count; index++ {
		role := "user"
		if index%2 == 1 {
			role = "assistant"
		}
		_, _ = fileStore.AddMessage(conversation.ID, role, strings.Repeat("context ", 20))
	}
	return fileStore, conversation, run
}

type compactionFaultStore struct {
	Store
	commitErr     error
	messagesErr   error
	latestErr     error
	duplicate     *domain.ContextCompaction
	failEventType domain.RunEventType
	eventErr      error
	listErr       error
}

func (s *compactionFaultStore) CommitContextCompaction(compaction domain.ContextCompaction, event domain.RunEvent) (domain.ContextCompaction, domain.RunEvent, error) {
	if s.commitErr != nil {
		return domain.ContextCompaction{}, domain.RunEvent{}, s.commitErr
	}
	if s.duplicate != nil {
		return *s.duplicate, domain.RunEvent{}, nil
	}
	return s.Store.CommitContextCompaction(compaction, event)
}

func (s *compactionFaultStore) ListMessages(conversationID string) ([]domain.Message, error) {
	if s.messagesErr != nil {
		return nil, s.messagesErr
	}
	return s.Store.ListMessages(conversationID)
}

func (s *compactionFaultStore) GetLatestContextCompaction(conversationID string) (domain.ContextCompaction, bool, error) {
	if s.latestErr != nil {
		return domain.ContextCompaction{}, false, s.latestErr
	}
	return s.Store.GetLatestContextCompaction(conversationID)
}

func (s *compactionFaultStore) CreateRunEvent(event domain.RunEvent) (domain.RunEvent, error) {
	if event.Type == s.failEventType && s.eventErr != nil {
		return domain.RunEvent{}, s.eventErr
	}
	return s.Store.CreateRunEvent(event)
}

func (s *compactionFaultStore) ListConversationRunEvents(conversationID string) ([]domain.RunEvent, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	return s.Store.ListConversationRunEvents(conversationID)
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
		SchemaVersion: domain.CurrentRuntimeSnapshotVersion, Mode: "single", RunBudget: &domain.RuntimeRunBudget{},
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
		ToolResultMaxTokens: 20, CompactionMode: contextassembly.CompactionModeAuto,
		CompactionSoftThreshold: 0.25, CompactionHardThreshold: 0.30,
		CompactionRecentTokens: 24, CompactionSummaryMaxTokens: 40,
		CompactionTimeoutMS: 1000,
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
