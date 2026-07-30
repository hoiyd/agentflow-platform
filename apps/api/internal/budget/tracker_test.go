package budget

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
)

type trackerStoreStub struct {
	budget  domain.RuntimeRunBudget
	entries []domain.RunUsageEntry
	err     error
	skip    bool
}

func (s *trackerStoreStub) ApplyRunUsage(entry domain.RunUsageEntry) (domain.RunUsageLedger, bool, error) {
	if s.err != nil {
		return BuildLedger(entry.RunID, s.budget, s.entries), false, s.err
	}
	if s.skip {
		return BuildLedger(entry.RunID, s.budget, s.entries), false, nil
	}
	s.entries = append(s.entries, entry)
	return BuildLedger(entry.RunID, s.budget, s.entries), true, nil
}

func (s *trackerStoreStub) GetRunUsageLedger(runID string) (domain.RunUsageLedger, bool, error) {
	return BuildLedger(runID, s.budget, s.entries), true, nil
}

type trackerEventSinkStub struct {
	events []domain.RunEvent
}

func (s *trackerEventSinkStub) Publish(_ context.Context, event domain.RunEvent) error {
	s.events = append(s.events, event)
	return nil
}

func TestTrackerRecordsModelAndToolUsage(t *testing.T) {
	limits := domain.RuntimeRunBudget{
		MaxModelCalls: 3, MaxToolCalls: 2, MaxCompletionTokens: 50, MaxTotalTokens: 100,
		MaxRuntimeMS: 10_000, InputCostPerMillionTokensMicros: 2_000_000,
		OutputCostPerMillionTokensMicros: 4_000_000,
	}
	store := &trackerStoreStub{budget: limits}
	sink := &trackerEventSinkStub{}
	run := trackerTestRun(limits)
	run.ActiveRuntimeMS = 100
	tracker := NewTracker(store, sink, run)
	ctx := WithScope(context.Background(), Scope{StageID: "stage-1", TurnID: "turn-1"})

	reservation, err := tracker.BeginModelCall(ctx, ModelCallEstimate{
		OperationID: "model-1", Purpose: domain.UsagePurposeRouter, Model: " test-model ", EstimatedPromptTokens: 10,
	})
	if err != nil {
		t.Fatalf("begin model call: %v", err)
	}
	if reservation.OperationID != "model-1" || reservation.Purpose != domain.UsagePurposeRouter || reservation.Model != "test-model" || reservation.MaxCompletionTokens != 50 {
		t.Fatalf("unexpected reservation: %#v", reservation)
	}
	if len(store.entries) != 1 || store.entries[0].StageID != "stage-1" || store.entries[0].TurnID != "turn-1" || store.entries[0].EstimatedCostMicros != 24 {
		t.Fatalf("unexpected reservation entry: %#v", store.entries)
	}

	if err := tracker.SettleModelCall(ctx, reservation, ModelUsage{PromptTokens: 8, CompletionTokens: 7}); err != nil {
		t.Fatalf("settle model call: %v", err)
	}
	if len(store.entries) != 2 || store.entries[1].Kind != domain.UsageModelSettlement || store.entries[1].TotalTokens != 15 || store.entries[1].EstimatedCostMicros != 44 {
		t.Fatalf("unexpected settlement entry: %#v", store.entries)
	}

	if err := tracker.RecordToolCall(ctx, ToolCall{Purpose: "unsupported", ToolName: " calculator "}); err != nil {
		t.Fatalf("record tool call: %v", err)
	}
	if len(store.entries) != 3 || !strings.HasPrefix(store.entries[2].OperationID, "tool_") || store.entries[2].Purpose != domain.UsagePurposePrimary || store.entries[2].ToolName != "calculator" {
		t.Fatalf("unexpected tool entry: %#v", store.entries)
	}
	if len(sink.events) != 3 {
		t.Fatalf("expected three usage events, got %#v", sink.events)
	}
	for _, event := range sink.events {
		if event.Type != domain.EventUsageRecorded || event.RunID != run.ID || event.ConversationID != run.ConversationID || event.StageID != "stage-1" || event.TurnID != "turn-1" {
			t.Fatalf("unexpected usage event: %#v", event)
		}
	}
}

func TestTrackerHandlesIdempotencyAndStoreErrors(t *testing.T) {
	limits := domain.RuntimeRunBudget{MaxModelCalls: 2}
	run := trackerTestRun(limits)

	t.Run("idempotent entry does not publish", func(t *testing.T) {
		store := &trackerStoreStub{budget: limits, skip: true}
		sink := &trackerEventSinkStub{}
		tracker := NewTracker(store, sink, run)
		if _, err := tracker.BeginModelCall(context.Background(), ModelCallEstimate{OperationID: "same"}); err != nil {
			t.Fatalf("begin model call: %v", err)
		}
		if len(sink.events) != 0 || len(store.entries) != 0 {
			t.Fatalf("idempotent usage should not publish or append: events=%#v entries=%#v", sink.events, store.entries)
		}
	})

	t.Run("exceeded store error is published", func(t *testing.T) {
		exceeded := &ExceededError{Resource: ResourceModelCalls, Limit: 2, Used: 2, Requested: 1, OperationID: "model-3", Purpose: domain.UsagePurposePrimary}
		store := &trackerStoreStub{budget: limits, err: exceeded}
		sink := &trackerEventSinkStub{}
		tracker := NewTracker(store, sink, run)
		_, err := tracker.BeginModelCall(context.Background(), ModelCallEstimate{OperationID: "model-3"})
		if !errors.Is(err, exceeded) {
			t.Fatalf("expected exceeded error, got %v", err)
		}
		if len(sink.events) != 1 || sink.events[0].Type != domain.EventBudgetExceeded || sink.events[0].Payload["resource"] != ResourceModelCalls {
			t.Fatalf("expected budget event, got %#v", sink.events)
		}
	})

	t.Run("ordinary store error is not a budget event", func(t *testing.T) {
		store := &trackerStoreStub{budget: limits, err: errors.New("storage unavailable")}
		sink := &trackerEventSinkStub{}
		tracker := NewTracker(store, sink, run)
		if err := tracker.RecordToolCall(context.Background(), ToolCall{OperationID: "tool-1"}); err == nil {
			t.Fatal("expected storage error")
		}
		if len(sink.events) != 0 {
			t.Fatalf("ordinary error should not publish a budget event: %#v", sink.events)
		}
	})
}

func TestTrackerRuntimeLimit(t *testing.T) {
	now := time.Now().UTC()
	limits := domain.RuntimeRunBudget{MaxRuntimeMS: 1_000}
	run := trackerTestRun(limits)
	run.Status = domain.RunRunning
	run.ActiveRuntimeMS = 800
	started := now.Add(-300 * time.Millisecond)
	run.ExecutionStartedAt = &started
	sink := &trackerEventSinkStub{}
	tracker := NewTracker(&trackerStoreStub{budget: limits}, sink, run)

	remaining, limited := tracker.RemainingRuntime(now)
	if !limited || remaining >= 0 {
		t.Fatalf("expected exhausted runtime, remaining=%s limited=%t", remaining, limited)
	}
	err := tracker.CheckRuntime(now)
	exceeded, ok := AsExceeded(err)
	if !ok || exceeded.Resource != ResourceRuntime || exceeded.Limit != 1_000 || exceeded.Used < 1_000 {
		t.Fatalf("unexpected runtime error: %#v", exceeded)
	}
	tracker.PublishExceeded(WithScope(context.Background(), Scope{StageID: "stage-runtime", TurnID: "turn-runtime"}), err)
	if len(sink.events) != 1 || sink.events[0].StageID != "stage-runtime" || sink.events[0].TurnID != "turn-runtime" {
		t.Fatalf("unexpected runtime event: %#v", sink.events)
	}

	if _, limited := (*Tracker)(nil).RemainingRuntime(now); limited {
		t.Fatal("nil tracker should not report a runtime limit")
	}
	unlimited := NewTracker(&trackerStoreStub{}, nil, domain.Run{ID: "run-unlimited"})
	if remaining, limited := unlimited.RemainingRuntime(now); limited || remaining != 0 {
		t.Fatalf("unexpected unlimited runtime: remaining=%s limited=%t", remaining, limited)
	}
}

func TestBudgetContextValuesRoundTrip(t *testing.T) {
	controller := &controllerStub{}
	ctx := context.Background()
	if FromContext(nil) != nil || OperationFromContext(nil) != "" || PurposeFromContext(nil) != domain.UsagePurposePrimary || ScopeFromContext(nil) != (Scope{}) {
		t.Fatal("nil context should return safe defaults")
	}
	if WithController(ctx, nil) != ctx {
		t.Fatal("nil controller should preserve context")
	}
	ctx = WithController(ctx, controller)
	ctx = WithOperation(ctx, " operation-1 ")
	ctx = WithPurpose(ctx, domain.UsagePurposeCompaction)
	ctx = WithScope(ctx, Scope{StageID: "stage-1", TurnID: "turn-1"})
	if FromContext(ctx) != controller || OperationFromContext(ctx) != "operation-1" || PurposeFromContext(ctx) != domain.UsagePurposeCompaction || ScopeFromContext(ctx).TurnID != "turn-1" {
		t.Fatalf("context values did not round trip")
	}
	if id := NewOperationID(" usage "); !strings.HasPrefix(id, "usage_") || len(id) <= len("usage_") {
		t.Fatalf("unexpected operation ID %q", id)
	}
}

type controllerStub struct{}

func (*controllerStub) BeginModelCall(context.Context, ModelCallEstimate) (ModelReservation, error) {
	return ModelReservation{}, nil
}
func (*controllerStub) SettleModelCall(context.Context, ModelReservation, ModelUsage) error {
	return nil
}
func (*controllerStub) RecordToolCall(context.Context, ToolCall) error { return nil }

func trackerTestRun(limits domain.RuntimeRunBudget) domain.Run {
	snapshot := domain.RuntimeSnapshot{RunBudget: &limits}
	return domain.Run{ID: "run-1", ConversationID: "conversation-1", Status: domain.RunWaitingForUser, RuntimeSnapshot: &snapshot}
}
