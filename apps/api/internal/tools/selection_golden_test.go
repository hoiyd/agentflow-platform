package tools_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"agentflow-platform/apps/api/internal/taskstate"
	"agentflow-platform/apps/api/internal/testsupport/tooltest"
	"agentflow-platform/apps/api/internal/tools"
)

func TestToolSelectionGoldenDatasetMatchesCurrentContracts(t *testing.T) {
	dataset := loadSelectionDataset(t)
	catalog := selectionCatalog(t)
	defaultCatalog := tools.DefaultCatalog()
	coveredTools := make(map[string]bool)
	coveredOutcomes := make(map[string]bool)
	for _, item := range dataset.Cases {
		candidate := candidateFromExpectation(item.Expected)
		if findings := tooltest.EvaluateSelection(catalog, item, candidate); len(findings) != 0 {
			t.Fatalf("case %s rejected expected candidate: %#v", item.ID, findings)
		}
		if item.Expected.Decision == "tool" {
			coveredTools[item.Expected.Tool] = true
			if _, executable := defaultCatalog.Installed(item.Expected.Tool); executable {
				observed := executeGoldenCall(defaultCatalog, item)
				if observed != item.Expected.Outcome {
					t.Fatalf("case %s outcome = %q, want %q", item.ID, observed, item.Expected.Outcome)
				}
			}
		}
		coveredOutcomes[item.Expected.Outcome] = true
	}
	for _, item := range catalog.List() {
		if !coveredTools[item.Name] {
			t.Fatalf("default Binding %q has no Tool selection case", item.Name)
		}
	}
	for _, outcome := range []string{"no_tool", "success", string(tools.ErrorInvalidArgs), string(tools.ErrorExecutionFailed)} {
		if !coveredOutcomes[outcome] {
			t.Fatalf("golden dataset has no %s outcome", outcome)
		}
	}
}

func TestToolSelectionSensorRejectsAdversarialCandidates(t *testing.T) {
	dataset := loadSelectionDataset(t)
	catalog := selectionCatalog(t)
	cases := indexSelectionCases(dataset)
	tests := []struct {
		name      string
		caseID    string
		candidate tooltest.SelectionCandidate
		wantCode  string
	}{
		{
			name: "unnecessary Tool", caseID: "no-tool-greeting",
			candidate: tooltest.SelectionCandidate{
				Decision: "tool", Tool: "calculator", Arguments: json.RawMessage(`{"expression":"1 + 1"}`), Outcome: "success",
			}, wantCode: "decision_mismatch",
		},
		{
			name: "similar but wrong Tool", caseID: "time-not-calculator",
			candidate: tooltest.SelectionCandidate{
				Decision: "tool", Tool: "calculator", Arguments: json.RawMessage(`{"expression":"8"}`), Outcome: "success",
			}, wantCode: "tool_mismatch",
		},
		{
			name: "invalid arguments claimed success", caseID: "calculator-invalid-arguments",
			candidate: tooltest.SelectionCandidate{
				Decision: "tool", Tool: "calculator", Arguments: json.RawMessage(`{}`), Outcome: "success", RecoveryAction: "correct_arguments",
			}, wantCode: "outcome_mismatch",
		},
		{
			name: "success without evidence", caseID: "calculator-required-evidence",
			candidate: tooltest.SelectionCandidate{
				Decision: "tool", Tool: "calculator", Arguments: json.RawMessage(`{"expression":"21 * 2"}`), Outcome: "success",
			}, wantCode: "required_evidence_missing",
		},
		{
			name: "schema-valid semantic misuse", caseID: "unsupported-stock-price",
			candidate: tooltest.SelectionCandidate{
				Decision: "tool", Tool: "calculator", Arguments: json.RawMessage(`{"expression":"latest stock price"}`), Outcome: "success",
			}, wantCode: "unexpected_tool",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			findings := tooltest.EvaluateSelection(catalog, cases[test.caseID], test.candidate)
			if !containsFinding(findings, test.wantCode) {
				t.Fatalf("findings = %#v, want %s", findings, test.wantCode)
			}
		})
	}
}

func selectionCatalog(t *testing.T) *tools.Catalog {
	t.Helper()
	catalog, err := tools.DefaultCatalog().CloneWith((&taskstate.Service{}).ToolBinding())
	if err != nil {
		t.Fatalf("build selection catalog: %v", err)
	}
	return catalog
}

func TestToolSelectionDatasetRejectsUnknownAndDuplicateCases(t *testing.T) {
	unknown := []byte(`{"schema_version":"tool-selection-v1","id":"dataset","unknown":true,"cases":[]}`)
	if _, err := tooltest.ParseSelectionDataset(unknown); err == nil {
		t.Fatal("unknown dataset field was accepted")
	}
	duplicate := []byte(`{
		"schema_version":"tool-selection-v1","id":"dataset","cases":[
			{"id":"same","task":"one","expected":{"decision":"no_tool","outcome":"no_tool"}},
			{"id":"same","task":"two","expected":{"decision":"no_tool","outcome":"no_tool"}}
		]
	}`)
	if _, err := tooltest.ParseSelectionDataset(duplicate); err == nil {
		t.Fatal("duplicate dataset case was accepted")
	}
	trailing := []byte(`{"schema_version":"tool-selection-v1","id":"dataset","cases":[{"id":"one","task":"hello","expected":{"decision":"no_tool","outcome":"no_tool"}}]} {}`)
	if _, err := tooltest.ParseSelectionDataset(trailing); err == nil {
		t.Fatal("trailing JSON was accepted")
	}
	unsupportedOutcome := []byte(`{"schema_version":"tool-selection-v1","id":"dataset","cases":[{"id":"one","task":"run","expected":{"decision":"tool","tool":"calculator","arguments":{},"outcome":"policy_denied"}}]}`)
	if _, err := tooltest.ParseSelectionDataset(unsupportedOutcome); err == nil {
		t.Fatal("unsupported outcome was accepted")
	}
}

func loadSelectionDataset(t *testing.T) tooltest.SelectionDataset {
	t.Helper()
	data, err := os.ReadFile("testdata/tool_selection_golden.json")
	if err != nil {
		t.Fatalf("read golden dataset: %v", err)
	}
	dataset, err := tooltest.ParseSelectionDataset(data)
	if err != nil {
		t.Fatalf("parse golden dataset: %v", err)
	}
	return dataset
}

func candidateFromExpectation(expected tooltest.SelectionExpectation) tooltest.SelectionCandidate {
	return tooltest.SelectionCandidate{
		Decision: expected.Decision, Tool: expected.Tool, Arguments: expected.Arguments,
		Outcome: expected.Outcome, Evidence: expected.RequiredEvidence, RecoveryAction: expected.RecoveryAction,
	}
}

func executeGoldenCall(catalog *tools.Catalog, item tooltest.SelectionCase) string {
	result := tools.NewExecutor(catalog, tools.ExecutorOptions{}).Execute(context.Background(), tools.ExecutionRequest{
		CallID: "golden-" + item.ID, Tool: item.Expected.Tool, Arguments: item.Expected.Arguments,
	})
	if result.Error != nil {
		return string(result.Error.Code)
	}
	return "success"
}

func indexSelectionCases(dataset tooltest.SelectionDataset) map[string]tooltest.SelectionCase {
	items := make(map[string]tooltest.SelectionCase, len(dataset.Cases))
	for _, item := range dataset.Cases {
		items[item.ID] = item
	}
	return items
}

func containsFinding(findings []tooltest.SelectionFinding, code string) bool {
	for _, finding := range findings {
		if finding.Code == code {
			return true
		}
	}
	return false
}
