package contexteval

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentflow-platform/apps/api/internal/contextassembly"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/evalreport"
)

func TestCanonicalContextQualityGateAndAblation(t *testing.T) {
	path := canonicalDatasetPath()
	compacted, err := Run(context.Background(), Options{DatasetPath: path, Strategy: StrategyCompacted, Revision: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	if !compacted.Gate.Passed || compacted.Summary.Samples != 6 || compacted.Summary.ExpectedErrors != 1 || compacted.Summary.RequiredFactRetention < 0.9 {
		t.Fatalf("canonical compacted strategy failed: %#v", compacted)
	}
	if compacted.ReportFormat != evalreport.Format || compacted.Config.FixtureScope == "" || compacted.Summary.NotEvaluated != 0 {
		t.Fatalf("report identity or scope missing: %#v", compacted)
	}
	var diagnostic, recovery *Sample
	for index := range compacted.Samples {
		switch compacted.Samples[index].CaseID {
		case "missing-required-source":
			diagnostic = &compacted.Samples[index]
		case "failed-compaction-raw-history-fallback":
			recovery = &compacted.Samples[index]
		}
	}
	if diagnostic == nil || diagnostic.Passed || diagnostic.Classification != ClassificationDiagnostic || recovery == nil || recovery.RecoveryPath != "raw_history_fallback" || !recovery.Passed {
		t.Fatalf("failure denominator or recovery path missing: diagnostic=%#v recovery=%#v", diagnostic, recovery)
	}
	encoded, err := json.Marshal(compacted)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "DEPLOY_ENV=staging") || compacted.Samples[0].ModelInput == nil || compacted.Samples[0].ModelInput.PayloadHash == "" {
		t.Fatalf("report leaked fixture content or lost model-input evidence: %s", encoded)
	}

	full, err := Run(context.Background(), Options{DatasetPath: path, Strategy: StrategyFullHistory, Revision: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	ApplyComparison(&compacted, full, true)
	if compacted.Comparison == nil || !compacted.Comparison.Comparable || len(compacted.Comparison.ChangedVariables) != 1 || len(compacted.Comparison.Regressions) != 0 || !compacted.Gate.Passed {
		t.Fatalf("strategy ablation failed: %#v", compacted.Comparison)
	}
	if compacted.Summary.MeanInputTokens >= full.Summary.MeanInputTokens || compacted.Summary.ForbiddenLeaks >= full.Summary.ForbiddenLeaks {
		t.Fatalf("fixture did not expose the expected controlled improvement: compacted=%#v full=%#v", compacted.Summary, full.Summary)
	}
}

func TestContextQualityFailuresStayInDenominator(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	report, err := Run(ctx, Options{DatasetPath: canonicalDatasetPath()})
	if err != nil {
		t.Fatal(err)
	}
	if report.Summary.NotEvaluated != report.Summary.Samples || report.Gate.Passed || report.Gate.BlockingFailures == 0 {
		t.Fatalf("canceled samples disappeared: %#v", report.Summary)
	}

	baseline := Report{Identity: evalreport.Identity{ReportFormat: evalreport.Format, DatasetID: "d", DatasetVersion: "1", DatasetHash: "h"},
		SchemaVersion: SchemaVersion, Config: Config{Strategy: StrategyFullHistory}, Summary: Summary{RequiredFactRetention: 1, PrefixSetHash: "stable"},
		Gate: evalreport.Gate{Passed: true}}
	candidate := baseline
	candidate.Config.Strategy = StrategyCompacted
	candidate.Summary.RequiredFactRetention = 0.5
	candidate.Summary.PrefixSetHash = "changed"
	ApplyComparison(&candidate, baseline, true)
	if candidate.Gate.Passed || candidate.Comparison == nil || len(candidate.Comparison.Regressions) != 2 {
		t.Fatalf("regression was not enforced: %#v", candidate.Comparison)
	}
	candidate = baseline
	candidate.DatasetHash = "different"
	if comparison := Compare(candidate, baseline, false); comparison.Comparable {
		t.Fatalf("changed dataset accepted: %#v", comparison)
	}
}

func TestContextDatasetAndOptionsValidation(t *testing.T) {
	if _, err := Run(context.Background(), Options{}); err == nil {
		t.Fatal("missing dataset accepted")
	}
	if _, err := Run(context.Background(), Options{DatasetPath: canonicalDatasetPath(), Strategy: "unknown"}); err == nil {
		t.Fatal("unknown strategy accepted")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "dataset.json")
	if err := os.WriteFile(path, []byte("not json"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(context.Background(), Options{DatasetPath: path}); err == nil {
		t.Fatal("malformed dataset accepted")
	}
	invalid := dataset{SchemaVersion: DatasetSchemaVersion, ID: "fixture", Version: "1", Model: "fixture",
		Cases: []evaluationCase{{ID: "case", Split: "holdout", Classification: ClassificationGating, System: "system", CurrentInput: "input",
			History: []fixtureMessage{{ID: "m1", Role: "user", Content: "content"}}, Compaction: &fixtureCompaction{Summary: "summary", SourceMessageIDs: []string{"missing"}}}}}
	content, err := json.Marshal(invalid)
	if err != nil || os.WriteFile(path, content, 0600) != nil {
		t.Fatal("write invalid fixture")
	}
	if _, err := Run(context.Background(), Options{DatasetPath: path}); err == nil {
		t.Fatal("unknown compaction source accepted")
	}
}

func TestContextEvaluationFailureBranches(t *testing.T) {
	config := contextassembly.DefaultConfig()
	base := evaluationCase{ID: "case", Split: "holdout", Classification: ClassificationGating,
		System: "system", CurrentInput: "input", ExpectedErrorCode: "wanted"}
	if sample := runSample(context.Background(), "fixture", config, StrategyCompacted, base); sample.Passed || sample.FailureReason != "expected error was not observed" {
		t.Fatalf("missing expected error passed: %#v", sample)
	}
	base.SystemRepeat = 100
	base.System = strings.Repeat("required protocol ", 20)
	base.ExpectedErrorCode = "different"
	config.ContextWindowTokens, config.OutputReserveTokens, config.SafetyMarginTokens = 100, 10, 10
	if sample := runSample(context.Background(), "fixture", config, StrategyCompacted, base); sample.Status != "failed" || sample.FailureReason != "unexpected error code" {
		t.Fatalf("wrong error passed: %#v", sample)
	}
	base.ExpectedErrorCode = ""
	if sample := runSample(context.Background(), "fixture", config, StrategyCompacted, base); sample.Status != "failed" || sample.ErrorCode != "context_input_budget_exceeded" {
		t.Fatalf("assembly failure hidden: %#v", sample)
	}
	if _, err := observeModelInput(contextassembly.Pack{}, contextassembly.Request{Tools: []contextassembly.Tool{{Definition: map[string]any{"invalid": func() {}}}}}); err == nil {
		t.Fatal("unencodable model input accepted")
	}

	failedGate := gate([]Sample{{CaseID: "failed", Classification: ClassificationGating, Status: "failed", ErrorCode: "boom"}})
	if failedGate.Passed || failedGate.Reasons[0] != "failed: boom" {
		t.Fatalf("error fallback missing: %#v", failedGate)
	}
	if emptyGate := gate([]Sample{{Classification: ClassificationDiagnostic}}); emptyGate.Passed {
		t.Fatal("empty gate passed")
	}
}

func TestContextDatasetValidationBoundaries(t *testing.T) {
	valid := func() dataset {
		return dataset{SchemaVersion: DatasetSchemaVersion, ID: "d", Version: "1", Model: "m", Cases: []evaluationCase{{
			ID: "case", Split: "holdout", Classification: ClassificationGating, System: "system", CurrentInput: "input",
			History: []fixtureMessage{{ID: "m1", Role: "user", Content: "content"}},
		}}}
	}
	tests := []func(*dataset){
		func(data *dataset) { data.SchemaVersion = "wrong" },
		func(data *dataset) { data.Cases = append(data.Cases, data.Cases[0]) },
		func(data *dataset) { data.Cases[0].Split = "training" },
		func(data *dataset) { data.Cases[0].Classification = "optional" },
		func(data *dataset) { data.Cases[0].System = "" },
		func(data *dataset) { data.Cases[0].SystemRepeat = 101 },
		func(data *dataset) { data.Cases[0].History[0].Role = "tool" },
		func(data *dataset) { data.Cases[0].Compaction = &fixtureCompaction{} },
		func(data *dataset) { data.Cases[0].Classification = ClassificationDiagnostic },
	}
	for index, mutate := range tests {
		data := valid()
		mutate(&data)
		if err := validateDataset(data); err == nil {
			t.Fatalf("invalid dataset %d accepted", index)
		}
	}

	path := filepath.Join(t.TempDir(), "large.json")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 1<<20+1)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadDataset(path); err == nil {
		t.Fatal("oversized dataset accepted")
	}
}

func TestCanonicalContextQualityReportArtifact(t *testing.T) {
	dir := os.Getenv("EVALUATION_REPORT_DIR")
	if dir == "" {
		return
	}
	report, err := Run(context.Background(), Options{DatasetPath: canonicalDatasetPath(), Strategy: StrategyCompacted, Revision: "ci"})
	if err != nil {
		t.Fatal(err)
	}
	content, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "context-quality-offline.json"), content, 0600); err != nil {
		t.Fatal(err)
	}
}

func canonicalDatasetPath() string {
	return filepath.Join("..", "..", "..", "..", "examples", "context", "golden-dataset.v1.json")
}

func TestSourceTokenShareUsesManifestSelection(t *testing.T) {
	sample := Sample{SourceTokenShare: map[string]float64{}}
	fillTokenMetrics(&sample, contextManifest(100), []string{"noise"})
	if sample.IrrelevantTokens != 20 || sample.SourceTokenShare[contextassembly.SourceHistory] != 0.2 {
		t.Fatalf("unexpected source metrics: %#v", sample)
	}
}

func contextManifest(total int) domain.ContextManifest {
	return domain.ContextManifest{EstimatedInputTokens: total, Entries: []domain.ContextManifestEntry{
		{Source: contextassembly.SourceHistory, ReferenceID: "noise", Selected: true, EstimatedTokens: 20},
		{Source: contextassembly.SourceHistory, ReferenceID: "excluded", Selected: false, EstimatedTokens: 30},
	}}
}
