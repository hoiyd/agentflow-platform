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
