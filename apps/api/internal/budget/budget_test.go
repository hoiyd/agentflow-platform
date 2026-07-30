package budget

import (
	"errors"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
)

func TestBuildLedgerSettlementReplacesReservation(t *testing.T) {
	now := time.Now().UTC()
	entries := []domain.RunUsageEntry{
		{ID: "reserve", RunID: "run-1", OperationID: "call-1", Kind: domain.UsageModelReservation, Purpose: domain.UsagePurposePrimary, ModelCalls: 1, PromptTokens: 100, CompletionTokens: 1, TotalTokens: 101, Estimated: true, Timestamp: now},
		{ID: "settle", RunID: "run-1", OperationID: "call-1", Kind: domain.UsageModelSettlement, Purpose: domain.UsagePurposePrimary, ModelCalls: 1, PromptTokens: 80, CompletionTokens: 20, TotalTokens: 100, Timestamp: now.Add(time.Millisecond)},
	}
	ledger := BuildLedger("run-1", domain.RuntimeRunBudget{}, entries)
	if ledger.Totals.ModelCalls != 1 || ledger.Totals.PromptTokens != 80 || ledger.Totals.CompletionTokens != 20 || ledger.Totals.TotalTokens != 100 || ledger.Totals.OpenReservations != 0 {
		t.Fatalf("settlement did not replace reservation: %#v", ledger.Totals)
	}
}

func TestBuildLedgerKeepsOpenReservationVisible(t *testing.T) {
	ledger := BuildLedger("run-1", domain.RuntimeRunBudget{}, []domain.RunUsageEntry{{
		ID: "reserve", RunID: "run-1", OperationID: "call-1", Kind: domain.UsageModelReservation,
		Purpose: domain.UsagePurposeCompaction, ModelCalls: 1, PromptTokens: 10, CompletionTokens: 1,
		TotalTokens: 11, Estimated: true, Timestamp: time.Now().UTC(),
	}})
	if ledger.Totals.OpenReservations != 1 || ledger.Totals.ModelCalls != 1 || ledger.Totals.TotalTokens != 11 {
		t.Fatalf("unexpected open reservation totals: %#v", ledger.Totals)
	}
}

func TestCheckReportsRequestedResource(t *testing.T) {
	err := Check(
		domain.RuntimeRunBudget{MaxModelCalls: 1, MaxTotalTokens: 10},
		domain.RunUsageTotals{ModelCalls: 1, TotalTokens: 9},
		domain.RunUsageTotals{ModelCalls: 1, TotalTokens: 2},
		"call-2", domain.UsagePurposeRouter,
	)
	exceeded, ok := AsExceeded(err)
	if !ok || exceeded.Resource != ResourceModelCalls || exceeded.Used != 1 || exceeded.Requested != 1 || exceeded.OperationID != "call-2" || exceeded.Purpose != domain.UsagePurposeRouter {
		t.Fatalf("unexpected exceeded error: %#v", exceeded)
	}
}

func TestActiveRuntimeExcludesPausedWallClock(t *testing.T) {
	now := time.Now().UTC()
	started := now.Add(-30 * time.Second)
	run := domain.Run{Status: domain.RunWaitingForUser, ActiveRuntimeMS: 1250, ExecutionStartedAt: &started}
	if got := ActiveRuntimeMS(run, now); got != 1250 {
		t.Fatalf("waiting run should use persisted active time only, got %d", got)
	}
	run.Status = domain.RunRunning
	if got := ActiveRuntimeMS(run, now); got < 31_000 || got > 31_500 {
		t.Fatalf("running segment was not included, got %d", got)
	}
}

func TestExceededErrorSupportsErrorsAs(t *testing.T) {
	want := &ExceededError{Resource: ResourceToolCalls, Limit: 2, Used: 2, Requested: 1}
	wrapped := errors.Join(errors.New("tool rejected"), want)
	got, ok := AsExceeded(wrapped)
	if !ok || got != want {
		t.Fatalf("errors.As failed: got=%#v ok=%t", got, ok)
	}
}

func TestTokenCostUsesIntegerMicrodollars(t *testing.T) {
	if cost := tokenCostMicros(1_000, 2_500_000); cost != 2_500 {
		t.Fatalf("expected 2500 microdollars, got %d", cost)
	}
}

func TestCheckCoversEveryLimitedResource(t *testing.T) {
	tests := []struct {
		name      string
		limits    domain.RuntimeRunBudget
		current   domain.RunUsageTotals
		requested domain.RunUsageTotals
		resource  Resource
	}{
		{name: "prompt tokens", limits: domain.RuntimeRunBudget{MaxPromptTokens: 10}, current: domain.RunUsageTotals{PromptTokens: 8}, requested: domain.RunUsageTotals{PromptTokens: 3}, resource: ResourcePromptTokens},
		{name: "completion tokens", limits: domain.RuntimeRunBudget{MaxCompletionTokens: 10}, current: domain.RunUsageTotals{CompletionTokens: 9}, requested: domain.RunUsageTotals{CompletionTokens: 2}, resource: ResourceCompletionTokens},
		{name: "total tokens", limits: domain.RuntimeRunBudget{MaxTotalTokens: 10}, current: domain.RunUsageTotals{TotalTokens: 7}, requested: domain.RunUsageTotals{TotalTokens: 4}, resource: ResourceTotalTokens},
		{name: "tool calls", limits: domain.RuntimeRunBudget{MaxToolCalls: 2}, current: domain.RunUsageTotals{ToolCalls: 2}, requested: domain.RunUsageTotals{ToolCalls: 1}, resource: ResourceToolCalls},
		{name: "estimated cost", limits: domain.RuntimeRunBudget{MaxEstimatedCostMicros: 100}, current: domain.RunUsageTotals{EstimatedCostMicros: 90}, requested: domain.RunUsageTotals{EstimatedCostMicros: 11}, resource: ResourceEstimatedCost},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			exceeded, ok := AsExceeded(Check(test.limits, test.current, test.requested, "operation", domain.UsagePurposeCompaction))
			if !ok || exceeded.Resource != test.resource || exceeded.OperationID != "operation" || exceeded.Purpose != domain.UsagePurposeCompaction {
				t.Fatalf("unexpected exceeded error: %#v", exceeded)
			}
		})
	}
	if err := Check(domain.RuntimeRunBudget{}, domain.RunUsageTotals{}, domain.RunUsageTotals{TotalTokens: 1_000}, "operation", domain.UsagePurposePrimary); err != nil {
		t.Fatalf("unlimited budget should pass: %v", err)
	}
}

func TestLedgerHelpersAndCompletionLimit(t *testing.T) {
	entry := domain.RunUsageEntry{ModelCalls: 1, ToolCalls: 2, PromptTokens: 3, CompletionTokens: 4, TotalTokens: 7, EstimatedCostMicros: 8}
	totals := EntryTotals(entry)
	if totals.ModelCalls != 1 || totals.ToolCalls != 2 || totals.TotalTokens != 7 || totals.EstimatedCostMicros != 8 {
		t.Fatalf("unexpected entry totals: %#v", totals)
	}
	if err := CheckTotals(domain.RuntimeRunBudget{MaxToolCalls: 1}, totals, "operation", domain.UsagePurposePrimary); err == nil {
		t.Fatal("expected totals check to enforce tool call limit")
	}

	reservation := domain.RunUsageEntry{PromptTokens: 10, CompletionTokens: 1, TotalTokens: 11, EstimatedCostMicros: 24}
	tests := []struct {
		name   string
		limits domain.RuntimeRunBudget
		totals domain.RunUsageTotals
		want   int
	}{
		{name: "unlimited", want: 0},
		{name: "completion", limits: domain.RuntimeRunBudget{MaxCompletionTokens: 20}, totals: domain.RunUsageTotals{CompletionTokens: 1}, want: 20},
		{name: "total", limits: domain.RuntimeRunBudget{MaxTotalTokens: 30}, totals: domain.RunUsageTotals{TotalTokens: 11}, want: 20},
		{name: "cost", limits: domain.RuntimeRunBudget{MaxEstimatedCostMicros: 100, InputCostPerMillionTokensMicros: 2_000_000, OutputCostPerMillionTokensMicros: 4_000_000}, totals: domain.RunUsageTotals{EstimatedCostMicros: 24}, want: 20},
		{name: "minimum one", limits: domain.RuntimeRunBudget{MaxCompletionTokens: 1}, totals: domain.RunUsageTotals{CompletionTokens: 10}, want: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := maxCompletionTokens(test.limits, test.totals, reservation); got != test.want {
				t.Fatalf("max completion tokens=%d want=%d", got, test.want)
			}
		})
	}
	if tokenCostMicros(0, 10) != 0 || tokenCostMicros(10, 0) != 0 {
		t.Fatal("zero tokens or price should have zero cost")
	}
	if normalizePurpose(domain.UsagePurposeRouter) != domain.UsagePurposeRouter || normalizePurpose("unknown") != domain.UsagePurposePrimary {
		t.Fatal("purpose normalization mismatch")
	}
}
