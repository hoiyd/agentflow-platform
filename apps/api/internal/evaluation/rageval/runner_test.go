package rageval

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/evaluation/evalreport"
	"agentflow-platform/apps/api/internal/modelprovider"
	"agentflow-platform/apps/api/internal/openai"
	"agentflow-platform/apps/api/internal/rag"
)

func TestOfflineRAGEvaluationIsReproducibleAndBounded(t *testing.T) {
	dataset, manifest := writeFixture(t, "Frankfurt", false)
	options := Options{DatasetPath: dataset, CorpusManifestPath: manifest, TopK: 1, Revision: "fixture"}
	first, err := Run(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Run(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Gate.Passed || first.DatasetHash != second.DatasetHash || first.Corpus.Hash != second.Corpus.Hash ||
		first.Summary.MRR != second.Summary.MRR || first.Summary.NDCG != second.Summary.NDCG {
		t.Fatalf("unstable report: first=%#v second=%#v", first, second)
	}
	if first.Config.MinimumEvidenceCoverage != 0.25 || first.Pipeline.RelevanceGate.MinimumEvidenceCoverage != 0.25 || len(first.SplitSummaries) != 1 {
		t.Fatalf("calibrated Gate configuration was not reported: %#v", first)
	}
	encoded, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "The control plane is in Frankfurt") || first.ReportFormat != evalreport.Format || first.Pipeline.Embedding.Provider != "local" {
		t.Fatalf("report leaked corpus content or lost identity: %s", encoded)
	}
}

func TestKnownBadAndInterruptedSamplesFailTheGate(t *testing.T) {
	dataset, manifest := writeFixture(t, "Tokyo", false)
	report, err := Run(context.Background(), Options{DatasetPath: dataset, CorpusManifestPath: manifest, TopK: 1})
	if err != nil {
		t.Fatal(err)
	}
	if report.Gate.Passed || report.Summary.Failed != 1 || report.Summary.GatingFailures != 1 {
		t.Fatalf("known-bad result passed: %#v", report)
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	report, err = Run(canceled, Options{DatasetPath: dataset, CorpusManifestPath: manifest, TopK: 1})
	if err != nil {
		t.Fatal(err)
	}
	if report.Summary.NotEvaluated != 1 || report.Gate.Passed {
		t.Fatalf("not-evaluated case disappeared: %#v", report)
	}
}

func TestSummaryDefinesNoAnswerAndFailureSemantics(t *testing.T) {
	result := summarize([]Sample{
		{Status: "completed", Passed: true, Answerable: false, PredictedNoAnswer: true, Classification: "diagnostic"},
		{Status: "completed", Answerable: true, PredictedNoAnswer: true, Classification: "gating"},
		{Status: "failed", Answerable: true, Classification: "gating"},
		{Status: "not_evaluated", Answerable: true, Classification: "gating"},
	})
	if result.Samples != 4 || result.Evaluated != 2 || result.Failed != 2 || result.NotEvaluated != 1 || result.AnswerableSamples != 3 {
		t.Fatalf("failure denominator changed: %#v", result)
	}
	if result.NoAnswerPrecision == nil || *result.NoAnswerPrecision != 0.5 || result.NoAnswerRecall == nil || *result.NoAnswerRecall != 1 {
		t.Fatalf("unexpected no-answer metrics: %#v", result)
	}
	empty := summarize([]Sample{{Status: "completed", Answerable: true}})
	if empty.NoAnswerPrecision != nil || empty.NoAnswerRecall != nil {
		t.Fatalf("undefined no-answer metric reported as zero: %#v", empty)
	}
}

func TestBaselineComparisonRejectsChangedInputsAndFindsRegressions(t *testing.T) {
	baseline := Report{Identity: evalreport.Identity{ReportFormat: evalreport.Format, EvaluationKind: "offline_rag", DatasetID: "d", DatasetVersion: "1", DatasetHash: "h"},
		SchemaVersion: SchemaVersion, Corpus: CorpusIdentity{DatasetID: "d", Version: "1", Hash: "c"}, Config: Config{TopK: 5},
		Summary: Summary{MRR: 1, NDCG: 1}, Gate: evalreport.Gate{Passed: true}}
	candidate := baseline
	candidate.Summary.MRR, candidate.Summary.NDCG, candidate.Summary.GatingFailures = 0.5, 0.5, 1
	ApplyComparison(&candidate, baseline, false)
	if candidate.Comparison == nil || !candidate.Comparison.Comparable || len(candidate.Comparison.Regressions) != 3 || candidate.Gate.Passed {
		t.Fatalf("regression was not enforced: %#v", candidate)
	}
	candidate = baseline
	candidate.DatasetHash = "changed"
	ApplyComparison(&candidate, baseline, false)
	if candidate.Comparison.Comparable || candidate.Gate.Passed {
		t.Fatalf("changed dataset was compared: %#v", candidate.Comparison)
	}
	candidate = baseline
	candidate.Config.TopK = 3
	comparison := Compare(candidate, baseline, true)
	if !comparison.Comparable || comparison.Mode != "single_variable_ablation" || len(comparison.ChangedVariables) != 1 {
		t.Fatalf("single-variable ablation rejected: %#v", comparison)
	}
	candidate = baseline
	candidate.Config.RetrievalMode = rag.RetrievalModeDenseOnly
	comparison = Compare(candidate, baseline, true)
	if !comparison.Comparable || len(comparison.ChangedVariables) != 1 || comparison.ChangedVariables[0] != "retrieval_mode" {
		t.Fatalf("retrieval mode was not treated as one ablation: %#v", comparison)
	}
	candidate = baseline
	candidate.EvaluationKind = "semantic_retrieval"
	if comparison := Compare(candidate, baseline, false); comparison.Comparable {
		t.Fatalf("different evaluation kinds compared: %#v", comparison)
	}
	candidate = baseline
	candidate.EmbeddingProfile.ConfiguredModel = "configured-alias"
	if comparison := Compare(candidate, baseline, true); !comparison.Comparable || len(comparison.ChangedVariables) != 1 || comparison.ChangedVariables[0] != "embedding" {
		t.Fatalf("embedding profile configuration mismatch was hidden: %#v", comparison)
	}
	candidate.Pipeline.SecurityPolicy = "changed"
	if comparison := Compare(candidate, baseline, true); comparison.Comparable {
		t.Fatalf("multi-variable ablation accepted: %#v", comparison)
	}
	candidate = baseline
	candidate.Config.MinimumEvidenceCoverage = 0.3
	candidate.Pipeline.RelevanceGate.ConfigVersion = "eval-evidence-coverage-0.30"
	candidate.Pipeline.RelevanceGate.MinimumEvidenceCoverage = 0.3
	comparison = Compare(candidate, baseline, true)
	if !comparison.Comparable || len(comparison.ChangedVariables) != 1 || comparison.ChangedVariables[0] != "minimum_evidence_coverage" {
		t.Fatalf("Gate threshold was not treated as one ablation: %#v", comparison)
	}
}

func TestCorpusValidationRejectsIdentityMismatchAndTraversal(t *testing.T) {
	dataset, manifest := writeFixture(t, "Frankfurt", false)
	content, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifest, []byte(strings.Replace(string(content), `"file":"doc.md"`, `"file":"../doc.md"`, 1)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(context.Background(), Options{DatasetPath: dataset, CorpusManifestPath: manifest, TopK: 1}); err == nil {
		t.Fatal("corpus traversal accepted")
	}
	if _, err := Run(context.Background(), Options{DatasetPath: dataset, CorpusManifestPath: manifest, TopK: 21}); err == nil {
		t.Fatal("invalid options accepted")
	}
}

func TestRunnerRejectsMalformedAssets(t *testing.T) {
	if _, err := Run(context.Background(), Options{}); err == nil {
		t.Fatal("missing paths accepted")
	}
	dir := t.TempDir()
	dataset := filepath.Join(dir, "dataset.json")
	manifest := filepath.Join(dir, "manifest.json")
	if err := os.WriteFile(dataset, []byte("not json"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(context.Background(), Options{DatasetPath: dataset, CorpusManifestPath: manifest}); err == nil {
		t.Fatal("malformed dataset accepted")
	}
	dataset, manifest = writeFixture(t, "Frankfurt", false)
	if err := os.WriteFile(manifest, []byte("not json"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(context.Background(), Options{DatasetPath: dataset, CorpusManifestPath: manifest}); err == nil {
		t.Fatal("malformed manifest accepted")
	}
}

func TestGateAndComparisonExplainAllBoundaries(t *testing.T) {
	diagnostic := gate([]Sample{{CaseID: "diagnostic", Classification: "diagnostic", Status: "completed", Passed: true}})
	if diagnostic.Passed || len(diagnostic.Reasons) != 1 {
		t.Fatalf("empty gate passed: %#v", diagnostic)
	}
	failed := gate([]Sample{{CaseID: "failed", Classification: "gating", Status: "failed", ErrorCode: "retrieval_failed"}})
	if failed.Passed || failed.Reasons[0] != "failed: retrieval_failed" {
		t.Fatalf("failed sample unexplained: %#v", failed)
	}

	baseline := Report{Identity: evalreport.Identity{ReportFormat: evalreport.Format, EvaluationKind: "offline_rag", DatasetID: "d", DatasetVersion: "1", DatasetHash: "h"},
		SchemaVersion: SchemaVersion, Corpus: CorpusIdentity{DatasetID: "d", Version: "1", Hash: "c"}}
	candidate := baseline
	candidate.Config = Config{TopK: 3, MinSimilarity: 0.2, Chunker: "changed"}
	candidate.Pipeline.Embedding.Provider = "changed"
	candidate.Pipeline.Fusion.Version = "changed"
	candidate.Pipeline.Reranker.Version = "changed"
	candidate.Pipeline.RelevanceGate.Version = "changed"
	candidate.Pipeline.SecurityPolicy = "changed"
	comparison := Compare(candidate, baseline, true)
	if comparison.Comparable || len(comparison.ChangedVariables) != 8 {
		t.Fatalf("configuration drift hidden: %#v", comparison)
	}
}

func TestSemanticEmbeddingProfileRunsThroughIsolatedIndex(t *testing.T) {
	dataset, manifest := writeFixture(t, "Frankfurt", false)
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"embedding":[1,0]}],"model":"semantic-v1"}`))
	}))
	defer server.Close()
	report, err := Run(context.Background(), Options{DatasetPath: dataset, CorpusManifestPath: manifest, TopK: 1, MinSimilarity: 0.5,
		RetrievalMode: rag.RetrievalModeDenseOnly, EmbeddingProfile: liveProfile(server.URL, 4, 1000, 1, time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Gate.Passed || report.EvaluationKind != "semantic_retrieval" || report.Pipeline.Embedding != (domain.EmbeddingInfo{Provider: "openai_compatible", Model: "semantic-v1", Dimensions: 2}) {
		t.Fatalf("semantic profile was not used: %#v", report)
	}
	if requests != 2 || report.EmbeddingProfile.Usage.PhysicalRequests != 2 || report.EmbeddingProfile.Usage.Index.PhysicalRequests != 1 || report.EmbeddingProfile.Usage.Query.PhysicalRequests != 1 {
		t.Fatalf("embedding usage was not separated by phase: requests=%d usage=%#v", requests, report.EmbeddingProfile.Usage)
	}
	if len(report.IndexBuild.IndexIdentity) != 1 || report.IndexBuild.IndexIdentity[0].EmbeddingDimensions != 2 || report.EmbeddingProfile.Usage.EstimatedCostUSD != nil {
		t.Fatalf("index identity or unknown cost was misreported: %#v", report)
	}
}

func TestSemanticEmbeddingRetriesConsumeCallBudget(t *testing.T) {
	dataset, manifest := writeFixture(t, "Frankfurt", false)
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		if requests == 1 {
			http.Error(w, `{"error":{"message":"retry","type":"server_error"}}`, http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"embedding":[1,0]}],"model":"semantic-v1"}`))
	}))
	defer server.Close()
	report, err := Run(context.Background(), Options{DatasetPath: dataset, CorpusManifestPath: manifest, TopK: 1,
		EmbeddingProfile: liveProfile(server.URL, 2, 1000, 2, time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	if requests != 2 || report.EmbeddingProfile.Usage.PhysicalRequests != 2 || report.EmbeddingProfile.Usage.RejectedRequests != 1 || report.Samples[0].ErrorCode != string(openai.ErrorRequestCallCapacity) || report.Gate.Passed {
		t.Fatalf("retry/call budget accounting changed: requests=%d report=%#v", requests, report)
	}
}

func TestSemanticEmbeddingFailuresStayInReportDenominator(t *testing.T) {
	dataset, manifest := writeFixture(t, "Frankfurt", false)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(50 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"embedding":[1,0]}],"model":"semantic-v1"}`))
	}))
	defer server.Close()

	for _, testCase := range []struct {
		name    string
		profile EmbeddingProfileOptions
		code    string
	}{
		{name: "input budget", profile: liveProfile(server.URL, 4, 1, 1, time.Second), code: "embedding_input_budget_exceeded"},
		{name: "timeout", profile: liveProfile(server.URL, 4, 1000, 1, 10*time.Millisecond), code: string(openai.ErrorTimeout)},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			report, err := Run(context.Background(), Options{DatasetPath: dataset, CorpusManifestPath: manifest, TopK: 1, EmbeddingProfile: testCase.profile})
			if err != nil {
				t.Fatal(err)
			}
			if report.IndexBuild.Status != "failed" || report.IndexBuild.ErrorCode != testCase.code || report.Summary.Samples != 1 || report.Summary.NotEvaluated != 1 || report.Gate.Passed {
				t.Fatalf("index failure disappeared from denominator: %#v", report)
			}
		})
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	report, err := Run(canceled, Options{DatasetPath: dataset, CorpusManifestPath: manifest, TopK: 1,
		EmbeddingProfile: liveProfile(server.URL, 4, 1000, 1, time.Second)})
	if err != nil || report.IndexBuild.ErrorCode != string(openai.ErrorCanceled) || report.Summary.NotEvaluated != 1 {
		t.Fatalf("cancellation was not retained in the report: err=%v report=%#v", err, report)
	}
}

func TestEmbeddingProfileValidationAndVectorContract(t *testing.T) {
	for _, profile := range []EmbeddingProfileOptions{
		{Name: "unknown"},
		{Name: EmbeddingProfileHash, Live: true},
		{Name: EmbeddingProfileOpenAICompatible, Live: true, BaseURL: "https://example.com", Model: "m", Dimensions: 2, MaxCalls: 1, MaxInputTokens: 1, RetryMaxAttempts: 1, Timeout: time.Second},
		{Name: EmbeddingProfileOllama, Live: true, BaseURL: "https://example.com", Model: "m", Dimensions: 2, MaxCalls: 1, MaxInputTokens: 1, RetryMaxAttempts: 1, Timeout: time.Second},
	} {
		if _, err := normalizeEmbeddingProfile(profile); err == nil {
			t.Fatalf("invalid profile accepted: %#v", profile)
		}
	}
	validOllama, err := normalizeEmbeddingProfile(EmbeddingProfileOptions{Name: EmbeddingProfileOllama, Live: true,
		APIKey: "must-not-be-forwarded", BaseURL: "http://localhost:11434/api/embed", Model: "embeddinggemma", Dimensions: 1536,
		MaxCalls: 10, MaxInputTokens: 1000, RetryMaxAttempts: 2, Timeout: time.Second})
	if err != nil || validOllama.APIKey != "" {
		t.Fatalf("valid Ollama profile was rejected or retained an unrelated API key: err=%v profile=%#v", err, validOllama)
	}
	embedder := &evaluationEmbedder{options: EmbeddingProfileOptions{Name: EmbeddingProfileOpenAICompatible, Model: "semantic-v1", Dimensions: 2}}
	for _, embedding := range []modelprovider.Embedding{
		{Vector: []float64{}, Provider: "openai_compatible", Model: "semantic-v1"},
		{Vector: []float64{math.NaN(), 0}, Provider: "openai_compatible", Model: "semantic-v1", Dimensions: 2},
		{Vector: []float64{1}, Provider: "openai_compatible", Model: "semantic-v1", Dimensions: 1},
		{Vector: []float64{1, 0}, Provider: "openai_compatible", Model: "changed", Dimensions: 2},
	} {
		if err := embedder.validate(embedding); err == nil {
			t.Fatalf("invalid vector accepted: %#v", embedding)
		}
	}
}

func liveProfile(baseURL string, maxCalls, maxTokens, attempts int, timeout time.Duration) EmbeddingProfileOptions {
	return EmbeddingProfileOptions{Name: EmbeddingProfileOpenAICompatible, Live: true, APIKey: "fixture-key", BaseURL: baseURL,
		Model: "semantic-v1", Dimensions: 2, MaxCalls: maxCalls, MaxInputTokens: maxTokens, RetryMaxAttempts: attempts, Timeout: timeout}
}

func TestCanonicalOfflineReportArtifact(t *testing.T) {
	dir := os.Getenv("EVALUATION_REPORT_DIR")
	if dir == "" {
		return
	}
	root := filepath.Join("..", "..", "..", "..")
	report, err := Run(context.Background(), Options{DatasetPath: filepath.Join(root, "examples", "knowledge", "golden-dataset.v1.json"),
		CorpusManifestPath: filepath.Join(root, "examples", "knowledge", "golden-v1", "corpus-manifest.v1.json"), TopK: 5, MinSimilarity: 0.15, Revision: "ci"})
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
	if err := os.WriteFile(filepath.Join(dir, "rag-offline.json"), content, 0600); err != nil {
		t.Fatal(err)
	}
}

func writeFixture(t *testing.T, expected string, nonBlocking bool) (string, string) {
	t.Helper()
	dir := t.TempDir()
	datasetPath := filepath.Join(dir, "dataset.json")
	manifestPath := filepath.Join(dir, "manifest.json")
	tags := ""
	if nonBlocking {
		tags = `,"tags":["non-blocking"]`
	}
	if err := os.WriteFile(filepath.Join(dir, "doc.md"), []byte("# Fact\n\nThe control plane is in Frankfurt."), 0600); err != nil {
		t.Fatal(err)
	}
	dataset := `{"schema_version":"rag-golden-dataset-v1","id":"fixture","version":"1","cases":[{"id":"fact","query":"Frankfurt","answerable":true,"expected_sources":[{"source_uri":"doc.md","content_contains":["` + expected + `"]}]` + tags + `}]}`
	if err := os.WriteFile(datasetPath, []byte(dataset), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, []byte(`{"dataset_id":"fixture","version":"1","documents":[{"file":"doc.md","title":"Fact"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	return datasetPath, manifestPath
}
