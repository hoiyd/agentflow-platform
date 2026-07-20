package budget

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/domain"
)

type Resource string

const (
	ResourceModelCalls       Resource = "model_calls"
	ResourcePromptTokens     Resource = "prompt_tokens"
	ResourceCompletionTokens Resource = "completion_tokens"
	ResourceTotalTokens      Resource = "total_tokens"
	ResourceToolCalls        Resource = "tool_calls"
	ResourceRuntime          Resource = "runtime_ms"
	ResourceEstimatedCost    Resource = "estimated_cost_micros"
)

type ExceededError struct {
	Resource    Resource
	Limit       int64
	Used        int64
	Requested   int64
	OperationID string
	Purpose     domain.RunUsagePurpose
}

func (e *ExceededError) Error() string {
	return fmt.Sprintf("run budget exceeded: resource=%s limit=%d used=%d requested=%d", e.Resource, e.Limit, e.Used, e.Requested)
}

func AsExceeded(err error) (*ExceededError, bool) {
	var exceeded *ExceededError
	ok := errors.As(err, &exceeded)
	return exceeded, ok
}

type Store interface {
	ApplyRunUsage(domain.RunUsageEntry) (domain.RunUsageLedger, bool, error)
	GetRunUsageLedger(string) (domain.RunUsageLedger, bool, error)
}

type EventSink interface {
	Publish(context.Context, domain.RunEvent) error
}

type ModelCallEstimate struct {
	OperationID           string
	Purpose               domain.RunUsagePurpose
	Model                 string
	EstimatedPromptTokens int
}

type ModelReservation struct {
	OperationID           string
	Purpose               domain.RunUsagePurpose
	Model                 string
	EstimatedPromptTokens int
	EstimatedCostMicros   int64
	MaxCompletionTokens   int
}

type ModelUsage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	Estimated        bool
}

type ToolCall struct {
	OperationID string
	Purpose     domain.RunUsagePurpose
	ToolName    string
}

type Controller interface {
	BeginModelCall(context.Context, ModelCallEstimate) (ModelReservation, error)
	SettleModelCall(context.Context, ModelReservation, ModelUsage) error
	RecordToolCall(context.Context, ToolCall) error
}

type Tracker struct {
	store  Store
	sink   EventSink
	run    domain.Run
	budget domain.RuntimeRunBudget
}

func NewTracker(store Store, sink EventSink, run domain.Run) *Tracker {
	limits := domain.RuntimeRunBudget{}
	if run.RuntimeSnapshot != nil && run.RuntimeSnapshot.RunBudget != nil {
		limits = *run.RuntimeSnapshot.RunBudget
	}
	return &Tracker{store: store, sink: sink, run: run, budget: limits}
}

func (t *Tracker) BeginModelCall(ctx context.Context, estimate ModelCallEstimate) (ModelReservation, error) {
	if err := t.CheckRuntime(time.Now().UTC()); err != nil {
		t.publishExceeded(ctx, err)
		return ModelReservation{}, err
	}
	operationID := strings.TrimSpace(estimate.OperationID)
	if operationID == "" {
		operationID = NewOperationID("model")
	}
	purpose := normalizePurpose(estimate.Purpose)
	promptTokens := max(0, estimate.EstimatedPromptTokens)
	entry := t.entry(ctx, domain.UsageModelReservation, operationID, purpose)
	entry.Model = strings.TrimSpace(estimate.Model)
	entry.ModelCalls = 1
	entry.PromptTokens = promptTokens
	entry.CompletionTokens = 1
	entry.TotalTokens = promptTokens + 1
	entry.EstimatedCostMicros = tokenCostMicros(promptTokens, t.budget.InputCostPerMillionTokensMicros) +
		tokenCostMicros(1, t.budget.OutputCostPerMillionTokensMicros)
	entry.Estimated = true
	ledger, applied, err := t.store.ApplyRunUsage(entry)
	if err != nil {
		t.publishExceeded(ctx, err)
		return ModelReservation{}, err
	}
	if applied {
		t.publishUsage(ctx, entry, ledger)
	}
	return ModelReservation{
		OperationID: operationID, Purpose: purpose, Model: entry.Model,
		EstimatedPromptTokens: promptTokens, EstimatedCostMicros: entry.EstimatedCostMicros,
		MaxCompletionTokens: maxCompletionTokens(t.budget, ledger.Totals, entry),
	}, nil
}

func (t *Tracker) SettleModelCall(ctx context.Context, reservation ModelReservation, usage ModelUsage) error {
	promptTokens := max(0, usage.PromptTokens)
	completionTokens := max(0, usage.CompletionTokens)
	totalTokens := usage.TotalTokens
	if totalTokens <= 0 {
		totalTokens = promptTokens + completionTokens
	}
	entry := t.entry(ctx, domain.UsageModelSettlement, reservation.OperationID, normalizePurpose(reservation.Purpose))
	entry.Model = reservation.Model
	entry.ModelCalls = 1
	entry.PromptTokens = promptTokens
	entry.CompletionTokens = completionTokens
	entry.TotalTokens = max(totalTokens, promptTokens+completionTokens)
	entry.EstimatedCostMicros = tokenCostMicros(promptTokens, t.budget.InputCostPerMillionTokensMicros) +
		tokenCostMicros(completionTokens, t.budget.OutputCostPerMillionTokensMicros)
	entry.Estimated = usage.Estimated
	ledger, applied, err := t.store.ApplyRunUsage(entry)
	if applied {
		t.publishUsage(ctx, entry, ledger)
	}
	if err != nil {
		t.publishExceeded(ctx, err)
	}
	return err
}

func (t *Tracker) RecordToolCall(ctx context.Context, call ToolCall) error {
	if err := t.CheckRuntime(time.Now().UTC()); err != nil {
		t.publishExceeded(ctx, err)
		return err
	}
	operationID := strings.TrimSpace(call.OperationID)
	if operationID == "" {
		operationID = NewOperationID("tool")
	}
	entry := t.entry(ctx, domain.UsageToolExecution, operationID, normalizePurpose(call.Purpose))
	entry.ToolName = strings.TrimSpace(call.ToolName)
	entry.ToolCalls = 1
	ledger, applied, err := t.store.ApplyRunUsage(entry)
	if err != nil {
		t.publishExceeded(ctx, err)
		return err
	}
	if applied {
		t.publishUsage(ctx, entry, ledger)
	}
	return nil
}

func (t *Tracker) RemainingRuntime(now time.Time) (time.Duration, bool) {
	if t == nil || t.budget.MaxRuntimeMS <= 0 {
		return 0, false
	}
	used := ActiveRuntimeMS(t.run, now)
	return time.Duration(t.budget.MaxRuntimeMS-used) * time.Millisecond, true
}

func (t *Tracker) CheckRuntime(now time.Time) error {
	remaining, limited := t.RemainingRuntime(now)
	if !limited || remaining > 0 {
		return nil
	}
	return &ExceededError{Resource: ResourceRuntime, Limit: t.budget.MaxRuntimeMS, Used: ActiveRuntimeMS(t.run, now)}
}

func (t *Tracker) PublishExceeded(ctx context.Context, err error) { t.publishExceeded(ctx, err) }

func ActiveRuntimeMS(run domain.Run, now time.Time) int64 {
	used := max(int64(0), run.ActiveRuntimeMS)
	if run.Status == domain.RunRunning && run.ExecutionStartedAt != nil {
		used += max(int64(0), now.Sub(*run.ExecutionStartedAt).Milliseconds())
	}
	return used
}

func (t *Tracker) entry(ctx context.Context, kind domain.RunUsageEntryKind, operationID string, purpose domain.RunUsagePurpose) domain.RunUsageEntry {
	scope := ScopeFromContext(ctx)
	return domain.RunUsageEntry{
		ID: NewOperationID("usage"), RunID: t.run.ID, OperationID: operationID,
		StageID: scope.StageID, TurnID: scope.TurnID, Kind: kind, Purpose: purpose,
		Timestamp: time.Now().UTC(),
	}
}

func (t *Tracker) publishUsage(ctx context.Context, entry domain.RunUsageEntry, ledger domain.RunUsageLedger) {
	if t == nil || t.sink == nil {
		return
	}
	_ = t.sink.Publish(context.WithoutCancel(ctx), domain.RunEvent{
		Type: domain.EventUsageRecorded, RunID: t.run.ID, ConversationID: t.run.ConversationID,
		StageID: entry.StageID, TurnID: entry.TurnID, Timestamp: time.Now().UTC(),
		Payload: map[string]any{
			"usage_entry_id": entry.ID, "operation_id": entry.OperationID, "kind": entry.Kind,
			"purpose": entry.Purpose, "model": entry.Model, "tool_name": entry.ToolName,
			"model_calls": ledger.Totals.ModelCalls, "tool_calls": ledger.Totals.ToolCalls,
			"prompt_tokens": ledger.Totals.PromptTokens, "completion_tokens": ledger.Totals.CompletionTokens,
			"total_tokens": ledger.Totals.TotalTokens, "estimated_cost_micros": ledger.Totals.EstimatedCostMicros,
			"open_reservations": ledger.Totals.OpenReservations, "usage_estimated": entry.Estimated,
		},
	})
}

func (t *Tracker) publishExceeded(ctx context.Context, err error) {
	exceeded, ok := AsExceeded(err)
	if !ok || t == nil || t.sink == nil {
		return
	}
	scope := ScopeFromContext(ctx)
	_ = t.sink.Publish(context.WithoutCancel(ctx), domain.RunEvent{
		Type: domain.EventBudgetExceeded, RunID: t.run.ID, ConversationID: t.run.ConversationID,
		StageID: scope.StageID, TurnID: scope.TurnID, Timestamp: time.Now().UTC(),
		Payload: map[string]any{
			"resource": exceeded.Resource, "limit": exceeded.Limit, "used": exceeded.Used,
			"requested": exceeded.Requested, "operation_id": exceeded.OperationID, "purpose": exceeded.Purpose,
		},
	})
}
