package tools_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/testsupport/tooltest"
	"agentflow-platform/apps/api/internal/tools"
)

func TestExecutorFaultHarness(t *testing.T) {
	tooltest.RunExecutorFaultHarness(t)
}

func TestEffectGateExposesIntentEffectAndSettlementBoundaries(t *testing.T) {
	fixture := tooltest.NewEffectGateFixture()
	catalog, _, err := tooltest.NewAuthorizedCatalog(fixture.Binding("write_record"))
	if err != nil {
		t.Fatalf("new catalog: %v", err)
	}
	executor := tools.NewExecutor(catalog, tools.ExecutorOptions{EffectJournal: fixture})
	resultCh := make(chan tools.ExecutionResult, 1)
	go func() {
		resultCh <- executor.Execute(context.Background(), tools.ExecutionRequest{
			CallID: "call-1", RunID: "run-1", StageID: "stage-1", TurnID: "turn-1",
			Tool: "write_record", Arguments: json.RawMessage(`{"value":"a"}`),
		})
	}()

	waitForPhase(t, fixture, tooltest.EffectIntentPersisted)
	if record := fixture.Record(); record.Status != domain.ToolEffectExecuting || fixture.HandlerCalls() != 0 {
		t.Fatalf("intent boundary is not durable-before-effect: record=%#v calls=%d", record, fixture.HandlerCalls())
	}
	fixture.Release(tooltest.EffectIntentPersisted)

	waitForPhase(t, fixture, tooltest.EffectApplied)
	if record := fixture.Record(); record.Status != domain.ToolEffectExecuting || fixture.HandlerCalls() != 1 {
		t.Fatalf("effect boundary settled too early: record=%#v calls=%d", record, fixture.HandlerCalls())
	}
	fixture.Release(tooltest.EffectApplied)

	waitForPhase(t, fixture, tooltest.EffectSettlementPending)
	if record := fixture.Record(); record.Status != domain.ToolEffectExecuting {
		t.Fatalf("settlement gate did not expose pending state: %#v", record)
	}
	fixture.Release(tooltest.EffectSettlementPending)

	select {
	case result := <-resultCh:
		if result.Error != nil || fixture.Record().Status != domain.ToolEffectCommitted {
			t.Fatalf("effect did not settle: result=%#v record=%#v", result, fixture.Record())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("effect execution did not finish")
	}
}

func TestEffectGateInjectsJournalFailuresWithoutNetwork(t *testing.T) {
	t.Run("intent failure", func(t *testing.T) {
		fixture := tooltest.NewEffectGateFixture()
		fixture.FailBegin(errors.New("intent unavailable"))
		result := executeEffectFixture(t, fixture)
		tooltest.AssertTypedFailure(t, result, tools.ErrorEffectJournal)
		if fixture.HandlerCalls() != 0 {
			t.Fatal("intent failure reached external effect")
		}
	})
	t.Run("settlement failure", func(t *testing.T) {
		fixture := tooltest.NewEffectGateFixture()
		fixture.FailComplete(errors.New("settlement unavailable"))
		for _, phase := range []tooltest.EffectPhase{tooltest.EffectIntentPersisted, tooltest.EffectApplied, tooltest.EffectSettlementPending} {
			fixture.Release(phase)
		}
		result := executeEffectFixture(t, fixture)
		tooltest.AssertTypedFailure(t, result, tools.ErrorEffectJournal)
		if record := fixture.Record(); record.Status != domain.ToolEffectNeedsReconciliation {
			t.Fatalf("uncertain effect did not require reconciliation: %#v", record)
		}
	})
}

func executeEffectFixture(t *testing.T, fixture *tooltest.EffectGateFixture) tools.ExecutionResult {
	t.Helper()
	catalog, _, err := tooltest.NewAuthorizedCatalog(fixture.Binding("write_record"))
	if err != nil {
		t.Fatalf("new catalog: %v", err)
	}
	return tools.NewExecutor(catalog, tools.ExecutorOptions{EffectJournal: fixture}).Execute(
		context.Background(),
		tools.ExecutionRequest{
			CallID: "call-1", RunID: "run-1", StageID: "stage-1",
			Tool: "write_record", Arguments: json.RawMessage(`{"value":"a"}`),
		},
	)
}

func waitForPhase(t *testing.T, fixture *tooltest.EffectGateFixture, phase tooltest.EffectPhase) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := fixture.Wait(ctx, phase); err != nil {
		t.Fatalf("wait for %s: %v", phase, err)
	}
}
