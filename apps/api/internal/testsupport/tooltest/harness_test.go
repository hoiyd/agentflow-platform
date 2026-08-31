package tooltest

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/tools"
)

func TestExecutorFaultHarness(t *testing.T) {
	RunExecutorFaultHarness(t)
}

func TestBindingContractHarness(t *testing.T) {
	RunBindingContract(t, BindingContract{
		Binding: tools.Binding{
			Descriptor: tools.Descriptor{
				Name: "write_value",
				Parameters: tools.ObjectSchema(map[string]any{
					"value": map[string]any{"type": "string", "minLength": 1},
				}, []string{"value"}),
				SideEffect: tools.SideEffectPolicy{Mode: tools.SideEffectExternal},
			},
			Handler: func(context.Context, json.RawMessage) (any, error) { return nil, nil },
		},
		ValidArguments: json.RawMessage(`{"value":"ok"}`),
		GoodResult:     map[string]any{"accepted": true},
		InvalidCalls: []InvalidCall{
			{Name: "missing value", Arguments: json.RawMessage(`{}`), WantArgumentCode: "required"},
		},
		BadResults: []BadResult{{Name: "not accepted", Value: map[string]any{"accepted": false}}},
		ValidateResult: func(value any) error {
			result, ok := value.(map[string]any)
			if !ok || result["accepted"] != true {
				return errors.New("result must be accepted")
			}
			return nil
		},
	})
}

func TestEffectGateFixtureLifecycle(t *testing.T) {
	fixture := NewEffectGateFixture()
	catalog, err := tools.NewCatalog(fixture.Binding("write_value"))
	if err != nil {
		t.Fatalf("new catalog: %v", err)
	}
	resultCh := make(chan tools.ExecutionResult, 1)
	go func() {
		resultCh <- tools.NewExecutor(catalog, tools.ExecutorOptions{EffectJournal: fixture}).Execute(
			context.Background(),
			tools.ExecutionRequest{
				CallID: "call-1", RunID: "run-1", StageID: "stage-1", Tool: "write_value",
				Arguments: json.RawMessage(`{"value":"ok"}`),
			},
		)
	}()

	for _, phase := range []EffectPhase{EffectIntentPersisted, EffectApplied, EffectSettlementPending} {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		if err := fixture.Wait(ctx, phase); err != nil {
			cancel()
			t.Fatalf("wait for %s: %v", phase, err)
		}
		cancel()
		if record := fixture.Record(); record.Status != domain.ToolEffectExecuting {
			t.Fatalf("record at %s = %#v", phase, record)
		}
		fixture.Release(phase)
	}
	result := <-resultCh
	if result.Error != nil || fixture.Record().Status != domain.ToolEffectCommitted || fixture.HandlerCalls() != 1 {
		t.Fatalf("effect fixture did not commit once: result=%#v record=%#v calls=%d", result, fixture.Record(), fixture.HandlerCalls())
	}
}

func TestEffectGateFixtureFailureInjection(t *testing.T) {
	beginFailure := NewEffectGateFixture()
	beginFailure.FailBegin(errors.New("intent failed"))
	result := executeEffectGateFixture(t, beginFailure)
	AssertTypedFailure(t, result, tools.ErrorEffectJournal)
	if beginFailure.HandlerCalls() != 0 {
		t.Fatal("intent failure reached handler")
	}

	completeFailure := NewEffectGateFixture()
	completeFailure.FailComplete(errors.New("settlement failed"))
	for _, phase := range []EffectPhase{EffectIntentPersisted, EffectApplied, EffectSettlementPending} {
		completeFailure.Release(phase)
	}
	result = executeEffectGateFixture(t, completeFailure)
	AssertTypedFailure(t, result, tools.ErrorEffectJournal)
	if completeFailure.Record().Status != domain.ToolEffectNeedsReconciliation {
		t.Fatalf("settlement failure was not marked for reconciliation: %#v", completeFailure.Record())
	}
}

func TestMemoryEffectJournalMarksUncertainEffects(t *testing.T) {
	journal := newMemoryEffectJournal()
	record, started, err := journal.BeginToolEffect(domain.ToolEffectRecord{IdempotencyKey: "effect-1"})
	if err != nil || !started || record.Status != domain.ToolEffectExecuting {
		t.Fatalf("begin effect: record=%#v started=%t err=%v", record, started, err)
	}
	record, err = journal.MarkToolEffectNeedsReconciliation("effect-1", "settlement failed")
	if err != nil || record.Status != domain.ToolEffectNeedsReconciliation || record.Error != "settlement failed" {
		t.Fatalf("mark reconciliation: record=%#v err=%v", record, err)
	}
}

func TestSelectionDatasetAndEvaluator(t *testing.T) {
	dataset, err := ParseSelectionDataset([]byte(`{
		"schema_version":"tool-selection-v1",
		"id":"self-test",
		"cases":[
			{"id":"no-tool","task":"say hello","expected":{"decision":"no_tool","outcome":"no_tool"}},
			{"id":"tool","task":"look up value","expected":{"decision":"tool","tool":"lookup","arguments":{"query":"value"},"outcome":"success","required_evidence":["value"]}}
		]
	}`))
	if err != nil {
		t.Fatalf("parse dataset: %v", err)
	}
	catalog, err := tools.NewCatalog(tools.Binding{
		Descriptor: tools.Descriptor{
			Name: "lookup", Parameters: tools.ObjectSchema(map[string]any{
				"query": map[string]any{"type": "string", "minLength": 1},
			}, []string{"query"}),
		},
		Handler: func(context.Context, json.RawMessage) (any, error) { return nil, nil },
	})
	if err != nil {
		t.Fatalf("new catalog: %v", err)
	}
	if findings := EvaluateSelection(catalog, dataset.Cases[0], SelectionCandidate{Decision: "no_tool", Outcome: "no_tool"}); len(findings) != 0 {
		t.Fatalf("valid no-Tool candidate rejected: %#v", findings)
	}
	noToolFindings := EvaluateSelection(catalog, dataset.Cases[0], SelectionCandidate{
		Decision: "tool", Tool: "lookup", Arguments: json.RawMessage(`{"query":"hello"}`), Outcome: "success",
	})
	for _, code := range []string{"decision_mismatch", "unexpected_tool", "outcome_mismatch"} {
		if !hasFinding(noToolFindings, code) {
			t.Fatalf("bad no-Tool candidate is missing %s: %#v", code, noToolFindings)
		}
	}
	valid := SelectionCandidate{
		Decision: "tool", Tool: "lookup", Arguments: json.RawMessage(`{"query":"value"}`),
		Outcome: "success", Evidence: []string{"value"},
	}
	if findings := EvaluateSelection(catalog, dataset.Cases[1], valid); len(findings) != 0 {
		t.Fatalf("valid Tool candidate rejected: %#v", findings)
	}
	bad := SelectionCandidate{Decision: "tool", Tool: "lookup", Arguments: json.RawMessage(`{}`), Outcome: "success"}
	findings := EvaluateSelection(catalog, dataset.Cases[1], bad)
	if !hasFinding(findings, "argument_contract_failed") || !hasFinding(findings, "required_evidence_missing") {
		t.Fatalf("bad candidate escaped deterministic sensors: %#v", findings)
	}
	invalidArgumentsCase := dataset.Cases[1]
	invalidArgumentsCase.Expected.Outcome = string(tools.ErrorInvalidArgs)
	invalidArgumentsCase.Expected.RecoveryAction = "correct_arguments"
	findings = EvaluateSelection(catalog, invalidArgumentsCase, valid)
	if !hasFinding(findings, "argument_outcome_mismatch") || !hasFinding(findings, "outcome_mismatch") || !hasFinding(findings, "recovery_mismatch") {
		t.Fatalf("invalid-argument expectation escaped sensors: %#v", findings)
	}
}

func TestSelectionDatasetRejectsMalformedContracts(t *testing.T) {
	tests := [][]byte{
		[]byte(`{}`),
		[]byte(`{"schema_version":"tool-selection-v1","id":"x","cases":[]}`),
		[]byte(`{"schema_version":"tool-selection-v1","id":"x","cases":[{"id":"x","task":"t","expected":{"decision":"unsupported","outcome":"no_tool"}}]}`),
		[]byte(`{"schema_version":"tool-selection-v1","id":"x","cases":[{"id":"x","task":"t","expected":{"decision":"no_tool","tool":"lookup","outcome":"no_tool"}}]}`),
		[]byte(`{"schema_version":"tool-selection-v1","id":"x","cases":[{"id":"x","task":"t","expected":{"decision":"tool","tool":"lookup","arguments":{},"outcome":"policy_denied"}}]}`),
		[]byte(`{"schema_version":"tool-selection-v1","id":"x","cases":[{"id":"x","task":"t","expected":{"decision":"no_tool","outcome":"no_tool"}}]} {}`),
	}
	for index, data := range tests {
		if _, err := ParseSelectionDataset(data); err == nil {
			t.Fatalf("malformed dataset %d was accepted", index)
		}
	}
}

func executeEffectGateFixture(t *testing.T, fixture *EffectGateFixture) tools.ExecutionResult {
	t.Helper()
	catalog, err := tools.NewCatalog(fixture.Binding("write_value"))
	if err != nil {
		t.Fatalf("new catalog: %v", err)
	}
	return tools.NewExecutor(catalog, tools.ExecutorOptions{EffectJournal: fixture}).Execute(
		context.Background(),
		tools.ExecutionRequest{
			CallID: "call-1", RunID: "run-1", StageID: "stage-1", Tool: "write_value",
			Arguments: json.RawMessage(`{"value":"ok"}`),
		},
	)
}

func hasFinding(findings []SelectionFinding, code string) bool {
	for _, finding := range findings {
		if finding.Code == code {
			return true
		}
	}
	return false
}
