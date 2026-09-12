package routeeval

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/agent"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/modelprovider"
)

func TestDeterministicBenchmarkPassesHoldoutAndProducesCalibration(t *testing.T) {
	report, err := Run(context.Background(), nil, Options{DatasetPath: datasetPath(), Revision: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Samples) != 16 || !report.Gate.Passed || report.Calibration == nil {
		t.Fatalf("unexpected report: samples=%+v gate=%+v calibration=%+v", report.Samples, report.Gate, report.Calibration)
	}
	holdout := report.Summary[SplitHoldout]
	if holdout.Samples != 9 || holdout.Top1AcceptableSelection.Denominator != 6 || holdout.NoRouteRecall.Denominator != 3 {
		t.Fatalf("unexpected holdout denominators: %+v", holdout)
	}
	if report.Config.AgentCatalogHash == "" || report.Config.PromptHash == "" || report.DatasetHash == "" || report.Config.Policy.MinimumScore != 6 {
		t.Fatal("report provenance is incomplete")
	}
	if report.Calibration.Recommendation != (Thresholds{MinimumScore: 4, MinimumScoreMargin: 1}) {
		t.Fatalf("unexpected calibration recommendation: %+v", report.Calibration.Recommendation)
	}
	content, err := os.ReadFile(legacyBaselinePath())
	if err != nil {
		t.Fatal(err)
	}
	var archive struct {
		Dataset struct {
			Hash string `json:"hash"`
		} `json:"dataset"`
		Baselines []struct {
			Accepted                bool   `json:"accepted"`
			ThresholdSource         string `json:"threshold_source"`
			MinimumScore            int    `json:"minimum_score"`
			MinimumScoreMargin      int    `json:"minimum_score_margin"`
			Top1AcceptableSelection Metric `json:"top_1_acceptable_selection"`
			UnsafeFalseRoute        Metric `json:"unsafe_false_route"`
			NoRouteRecall           Metric `json:"no_route_recall"`
			GatePassed              bool   `json:"gate_passed"`
		} `json:"baselines"`
	}
	if err := json.Unmarshal(content, &archive); err != nil {
		t.Fatal(err)
	}
	for _, baseline := range archive.Baselines {
		if !baseline.Accepted {
			continue
		}
		overall := report.Summary["overall"]
		if report.DatasetHash != archive.Dataset.Hash || report.Config.Policy.ThresholdSource != baseline.ThresholdSource ||
			report.Config.Policy.MinimumScore != baseline.MinimumScore || report.Config.Policy.MinimumScoreMargin != baseline.MinimumScoreMargin ||
			overall.Top1AcceptableSelection.Numerator != baseline.Top1AcceptableSelection.Numerator || overall.Top1AcceptableSelection.Denominator != baseline.Top1AcceptableSelection.Denominator ||
			overall.UnsafeFalseRoute.Numerator != baseline.UnsafeFalseRoute.Numerator || overall.UnsafeFalseRoute.Denominator != baseline.UnsafeFalseRoute.Denominator ||
			overall.NoRouteRecall.Numerator != baseline.NoRouteRecall.Numerator || overall.NoRouteRecall.Denominator != baseline.NoRouteRecall.Denominator || report.Gate.Passed != baseline.GatePassed {
			t.Fatalf("current routing result drifted from the accepted archived baseline: report=%+v baseline=%+v", overall, baseline)
		}
		return
	}
	t.Fatal("archive has no accepted routing baseline")
}

func TestLiveInvalidResponsesFallBackAndRemainInDenominator(t *testing.T) {
	client := fakeCompleter{completion: modelprovider.TextCompletion{Text: "not json", Model: "fixture-actual", Usage: modelprovider.Usage{PromptTokens: 10, CompletionTokens: 2, TotalTokens: 12}}}
	report, err := Run(context.Background(), client, Options{DatasetPath: datasetPath(),
		RouterMode: agent.RouterModeAuto, Trials: 1, MaxModelCalls: 20, MaxTotalTokens: 10000, Timeout: time.Second, Revision: "test", Model: "fixture", Provider: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	overall := report.Summary["overall"]
	if overall.InvalidResponse.Numerator != 14 || overall.InvalidResponse.Denominator != 14 || overall.FallbackRecovery.Numerator != 14 || overall.FallbackRecovery.Denominator != 14 || !report.Gate.Passed {
		t.Fatalf("invalid responses were dropped or not recovered: %+v gate=%+v", overall, report.Gate)
	}
	if report.Samples[0].ActualModel != "fixture-actual" {
		t.Fatal("actual model identity was not recorded")
	}
}

func TestLiveFailuresAndBudgetStopsRemainVisible(t *testing.T) {
	failing := fakeCompleter{err: errors.New("provider failed")}
	report, err := Run(context.Background(), failing, Options{DatasetPath: datasetPath(),
		RouterMode: agent.RouterModeAuto, Trials: 1, MaxModelCalls: 1, MaxTotalTokens: 10000, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if report.Summary["overall"].Failed != 1 || report.Summary["overall"].NotEvaluated != 15 || report.Gate.Passed {
		t.Fatalf("failed or skipped samples left the denominator: %+v", report.Summary["overall"])
	}
}

func TestCalibrationUsesConfidenceOnlyForLLMDecisions(t *testing.T) {
	samples := []Sample{
		{CaseID: "cal-reject", Split: SplitCalibration, Status: "completed", ExpectedOutcome: agent.AgentSelectionOutcomeNoSuitable,
			ActualOutcome: agent.AgentSelectionOutcomeNoSuitable, DecisionMode: "llm", ProposedAgentID: "a", TopScore: 80, ScoreMargin: 20, Confidence: 0.4, EligibleAgentIDs: []string{"a", "b"}},
		{CaseID: "cal-select", Split: SplitCalibration, Status: "completed", ExpectedOutcome: agent.AgentSelectionOutcomeSelected,
			ActualOutcome: agent.AgentSelectionOutcomeSelected, DecisionMode: "llm", ProposedAgentID: "b", SelectedAgentID: "b", TopScore: 90, ScoreMargin: 20, Confidence: 0.8,
			EligibleAgentIDs: []string{"a", "b"}, AcceptableAgentIDs: []string{"b"}, AcceptableSelection: true, EligibleRecall: true},
		{CaseID: "holdout-reject", Split: SplitHoldout, Status: "completed", ExpectedOutcome: agent.AgentSelectionOutcomeNoSuitable,
			ActualOutcome: agent.AgentSelectionOutcomeNoSuitable, DecisionMode: "llm", ProposedAgentID: "a", TopScore: 75, ScoreMargin: 10, Confidence: 0.3, EligibleAgentIDs: []string{"a", "b"}},
		{CaseID: "holdout-select", Split: SplitHoldout, Status: "completed", ExpectedOutcome: agent.AgentSelectionOutcomeSelected,
			ActualOutcome: agent.AgentSelectionOutcomeSelected, DecisionMode: "llm", ProposedAgentID: "b", SelectedAgentID: "b", TopScore: 95, ScoreMargin: 30, Confidence: 0.9,
			EligibleAgentIDs: []string{"a", "b"}, AcceptableAgentIDs: []string{"b"}, AcceptableSelection: true, EligibleRecall: true},
	}
	result := calibrate(samples)
	if result.Recommendation.MinimumLLMConfidence != 0.41 || result.Holdout.Top1AcceptableSelection.Numerator != 1 || result.Holdout.NoRouteRecall.Numerator != 1 {
		t.Fatalf("confidence was not calibrated on LLM decisions: %+v", result)
	}
}

func TestRejectsInvalidConfigurationAndDataset(t *testing.T) {
	if _, err := Run(context.Background(), nil, Options{DatasetPath: "missing"}); err == nil {
		t.Fatal("missing dataset accepted")
	}
	if _, err := Run(context.Background(), nil, Options{DatasetPath: datasetPath(), RouterMode: agent.RouterModeAuto}); err == nil {
		t.Fatal("live mode accepted without explicit client and budgets")
	}
	data := Dataset{SchemaVersion: DatasetSchemaVersion, ID: "fixture", Version: "1", Agents: []domain.Agent{{ID: "a"}, {ID: "b"}},
		Cases: []EvaluationCase{{ID: "case", Split: SplitCalibration, Task: "task", ExpectedOutcome: agent.AgentSelectionOutcomeNoSuitable}}}
	if err := validateDataset(&data); err == nil {
		t.Fatal("dataset without coverage and holdout accepted")
	}
}

func TestCanonicalRoutingReportArtifacts(t *testing.T) {
	dir := os.Getenv("EVALUATION_REPORT_DIR")
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	report, err := Run(context.Background(), nil, Options{DatasetPath: datasetPath(), Revision: "ci"})
	if err != nil {
		t.Fatal(err)
	}
	content, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "agent-routing.json"), content, 0600); err != nil {
		t.Fatal(err)
	}
}

func datasetPath() string {
	return filepath.Join("..", "..", "..", "..", "..", "examples", "routing", "golden-dataset.v1.json")
}

func legacyBaselinePath() string {
	return filepath.Join("..", "..", "..", "..", "..", "examples", "routing", "legacy-algorithm-baselines.json")
}

type fakeCompleter struct {
	completion modelprovider.TextCompletion
	err        error
}

func (f fakeCompleter) CompleteTextDetailed(context.Context, string, string) (modelprovider.TextCompletion, error) {
	return f.completion, f.err
}
