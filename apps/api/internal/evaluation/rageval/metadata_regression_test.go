package rageval

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// The same query must select different sources, with source/context metrics
// and actual constraints retained. A dropped filter must fail the Golden gate.
func TestGoldenCaseFiltersReachRetrievalAndContext(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"atlas.md":      "# Rollback\n\nROLLBACK-42 uses the Atlas production release procedure.",
		"boreal.md":     "# Rollback\n\nROLLBACK-42 uses the Boreal staging release procedure.",
		"manifest.json": `{"dataset_id":"metadata","version":"1","documents":[{"file":"atlas.md","title":"Atlas","metadata":{"product":"Atlas"}},{"file":"boreal.md","title":"Boreal","metadata":{"product":"Boreal"}}]}`,
		"dataset.json": `{"schema_version":"rag-golden-dataset-v1","id":"metadata","version":"1","cases":[
		{"id":"atlas","query":"ROLLBACK-42","answerable":true,"require_filter":true,"metadata":{"product":"Atlas"},"expected_sources":[{"source_uri":"atlas.md"}],"forbidden_sources":[{"source_uri":"boreal.md"}]},
		{"id":"boreal","query":"ROLLBACK-42","answerable":true,"require_filter":true,"metadata":{"product":"Boreal"},"expected_sources":[{"source_uri":"boreal.md"}],"forbidden_sources":[{"source_uri":"atlas.md"}]},
		{"id":"none","query":"ROLLBACK-42","answerable":false,"require_filter":true,"metadata":{"product":"Missing"},"forbidden_sources":[{"source_uri":"atlas.md"},{"source_uri":"boreal.md"}]}]}`,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	report, err := Run(context.Background(), Options{DatasetPath: filepath.Join(dir, "dataset.json"), CorpusManifestPath: filepath.Join(dir, "manifest.json"), TopK: 5})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Gate.Passed || report.Summary.Passed != 3 {
		t.Fatalf("filters were dropped: %+v", report)
	}
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Samples []struct {
			Evidence struct {
				Metadata         map[string]string `json:"metadata"`
				ContextLeakCount int               `json:"context_leak_count"`
				ExpectedSources  int               `json:"expected_sources"`
				MatchedSources   int               `json:"matched_sources"`
				ContextTokens    int               `json:"context_tokens"`
			} `json:"evidence"`
		} `json:"samples"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatal(err)
	}
	if e := wire.Samples[0].Evidence; e.Metadata["product"] != "Atlas" || e.ContextLeakCount != 0 || e.ExpectedSources != 1 || e.MatchedSources != 1 || e.ContextTokens < 1 {
		t.Fatalf("missing filter/context evidence: %s", data)
	}
}

func TestMetadataMetricDenominatorsKeepFailures(t *testing.T) {
	data := `[{
	 "status":"completed","answerable":true,"passed":true,"evidence":{"expected_sources":2,"matched_sources":1,"context_leak_count":1}
	},{"status":"failed","answerable":true,"evidence":{"expected_sources":1}},
	{"status":"not_evaluated","answerable":false}]`
	var samples []Sample
	if err := json.Unmarshal([]byte(data), &samples); err != nil {
		t.Fatal(err)
	}
	summary := summarize(samples)
	if summary.EligibleSourceRecall == nil || *summary.EligibleSourceRecall != 1.0/3 || summary.ContextLeakCount != 1 || summary.NotEvaluated != 1 || summary.Failed != 1 || summary.NoAnswerRecall == nil || *summary.NoAnswerRecall != 0 {
		t.Fatalf("incorrect denominators: %+v", summary)
	}
}

func TestMetadataGoldenDatasetGate(t *testing.T) {
	root := filepath.Join("..", "..", "..", "..", "..", "examples", "knowledge", "metadata-golden-v1")
	report, err := Run(t.Context(), Options{DatasetPath: filepath.Join(root, "dataset.json"), CorpusManifestPath: filepath.Join(root, "corpus-manifest.json"), TopK: 5, MinSimilarity: 0.15})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Gate.Passed || report.Summary.Passed < 10 || report.Summary.ContextLeakCount != 0 || report.Summary.EligibleSourceRecall == nil || *report.Summary.EligibleSourceRecall != 1 {
		t.Fatalf("metadata Golden failed: %+v reasons=%v", report.Summary, report.Gate.Reasons)
	}
	if report.Samples[0].Evidence.ContextSelection.ParentChunks == 0 || report.Samples[2].Evidence.ContextSelection.AdjacentChunks == 0 {
		t.Fatal("Golden did not exercise real parent and adjacent expansion")
	}
	compiler := jsonschema.NewCompiler()
	schemaPath, err := filepath.Abs(filepath.Join(root, "..", "..", "..", "docs", "schemas", "rag-golden-dataset-v1.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile(schemaPath)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "dataset.json"))
	if err != nil {
		t.Fatal(err)
	}
	var wire any
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatal(err)
	}
	if err := schema.Validate(wire); err != nil {
		t.Fatalf("published Golden schema rejects metadata profile: %v", err)
	}
	if dir := os.Getenv("EVALUATION_REPORT_DIR"); dir != "" {
		encoded, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "rag-metadata-offline.json"), encoded, 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMetadataGoldenDetectsDroppedFilter(t *testing.T) {
	root := filepath.Join("..", "..", "..", "..", "..", "examples", "knowledge", "metadata-golden-v1")
	dataset, _, err := loadDataset(filepath.Join(root, "dataset.json"))
	if err != nil {
		t.Fatal(err)
	}
	dataset.Cases = dataset.Cases[:1]
	// Deliberately opt out of the required-filter guard: the independent source
	// and Context oracle must still catch the broadened retrieval result.
	dataset.Cases[0].Metadata, dataset.Cases[0].RequireFilter = nil, false
	data, err := json.Marshal(dataset)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "dataset.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	report, err := Run(t.Context(), Options{DatasetPath: path, CorpusManifestPath: filepath.Join(root, "corpus-manifest.json"), TopK: 5, MinSimilarity: 0.15})
	if err != nil {
		t.Fatal(err)
	}
	if report.Gate.Passed || report.Summary.LeakCount == 0 || report.Summary.ContextLeakCount == 0 {
		t.Fatalf("dropped filter escaped independent oracle: %+v", report)
	}
}

func TestEligibilityPolicyAndContextLeakComparison(t *testing.T) {
	var baseline Report
	if err := json.Unmarshal([]byte(`{"report_format":"agentflow-evaluation-report-v1","schema_version":"rag-eval-v1","pipeline":{"eligibility_policy":"current"}}`), &baseline); err != nil {
		t.Fatal(err)
	}
	var candidate Report
	if err := json.Unmarshal([]byte(`{"report_format":"agentflow-evaluation-report-v1","schema_version":"rag-eval-v1","pipeline":{"eligibility_policy":"future"}}`), &candidate); err != nil {
		t.Fatal(err)
	}
	if Compare(candidate, baseline, false).Comparable {
		t.Fatal("eligibility policy drift was hidden")
	}
	candidate = baseline
	candidate.Summary.ContextLeakCount = 1
	comparison := Compare(candidate, baseline, false)
	if !comparison.Comparable || len(comparison.Regressions) != 1 || comparison.Deltas["context_leak_count"] != 1 {
		t.Fatalf("Context-only regression hidden: %+v", comparison)
	}
}
