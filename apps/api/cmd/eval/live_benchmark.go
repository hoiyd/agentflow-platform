package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/agent"
	"agentflow-platform/apps/api/internal/credential"
	"agentflow-platform/apps/api/internal/evaluation/evalreport"
	"agentflow-platform/apps/api/internal/evaluation/rageval"
	"agentflow-platform/apps/api/internal/evaluation/routeeval"
	"agentflow-platform/apps/api/internal/evaluation/tooleval"
)

const liveBenchmarkSchemaVersion = "case-001a-live-benchmark-v1"

type liveBenchmarkConfig struct {
	ChatModel               string        `json:"chat_model"`
	EmbeddingProfile        string        `json:"embedding_profile"`
	EmbeddingModel          string        `json:"embedding_model"`
	EmbeddingDimensions     int           `json:"embedding_dimensions"`
	Trials                  int           `json:"trials"`
	ToolMaxModelCalls       int           `json:"tool_max_model_calls"`
	ToolMaxTotalTokens      int           `json:"tool_max_total_tokens"`
	RouteMaxModelCalls      int           `json:"route_max_model_calls"`
	RouteMaxTotalTokens     int           `json:"route_max_total_tokens"`
	MaxEmbeddingCalls       int           `json:"max_embedding_calls"`
	MaxEmbeddingInputTokens int           `json:"max_embedding_input_tokens"`
	SampleTimeout           time.Duration `json:"sample_timeout_ns"`
	EmbeddingTimeout        time.Duration `json:"embedding_timeout_ns"`
}

type liveBenchmarkArtifact struct {
	Name           string `json:"name"`
	File           string `json:"file"`
	EvaluationKind string `json:"evaluation_kind"`
	Live           bool   `json:"live"`
	SHA256         string `json:"sha256"`
}

type binaryComparison struct {
	Pairs        int `json:"pairs"`
	Improved     int `json:"improved"`
	Unchanged    int `json:"unchanged"`
	Regressed    int `json:"regressed"`
	NotEvaluated int `json:"not_evaluated"`
}

type liveBenchmarkEvidence struct {
	BenchmarkTasks             int              `json:"benchmark_tasks"`
	DeclaredFailureScenarios   int              `json:"declared_failure_scenarios"`
	ObservedFailedOrSkipped    int              `json:"observed_failed_or_skipped"`
	ToolContextComparison      binaryComparison `json:"tool_context_comparison"`
	RouterModeComparison       binaryComparison `json:"router_mode_comparison"`
	NoGainOrRegressionObserved bool             `json:"no_gain_or_regression_observed"`
}

type liveBenchmarkManifest struct {
	evalreport.Identity
	SchemaVersion string                  `json:"schema_version"`
	Config        liveBenchmarkConfig     `json:"config"`
	Artifacts     []liveBenchmarkArtifact `json:"artifacts"`
	Evidence      liveBenchmarkEvidence   `json:"evidence"`
	CostSource    string                  `json:"cost_source"`
	Gate          evalreport.Gate         `json:"gate"`
}

type benchmarkSuiteIdentity struct {
	SchemaVersion    string             `json:"schema_version"`
	ID               string             `json:"id"`
	Version          string             `json:"version"`
	TaskSets         []benchmarkTaskSet `json:"task_sets"`
	FailureScenarios []json.RawMessage  `json:"failure_scenarios"`
}

type benchmarkTaskSet struct {
	Cases []json.RawMessage `json:"cases"`
}

func runBenchmark(ctx context.Context, args []string, out, stderr io.Writer) int {
	flags := flag.NewFlagSet("eval benchmark", flag.ContinueOnError)
	flags.SetOutput(stderr)
	live := flags.Bool("live", false, "explicitly authorize model and embedding requests")
	model := flags.String("model", "", "required chat model ID")
	baseURL := flags.String("base-url", "https://api.openai.com/v1", "OpenAI-compatible chat base URL")
	embeddingProfile := flags.String("embedding-profile", rageval.EmbeddingProfileOpenAICompatible, "embedding profile: openai_compatible or ollama")
	embeddingBaseURL := flags.String("embedding-base-url", "https://api.openai.com/v1", "OpenAI-compatible base URL or Ollama /api/embed URL")
	embeddingModel := flags.String("embedding-model", "", "required embedding model ID")
	embeddingDimensions := flags.Int("embedding-dimensions", 0, "required embedding dimensions")
	trials := flags.Int("trials", 3, "chat repetitions per task (3-20)")
	toolCalls := flags.Int("tool-max-model-calls", 0, "required Tool experiment model-call budget")
	toolTokens := flags.Int("tool-max-total-tokens", 0, "required Tool experiment token budget")
	routeCalls := flags.Int("route-max-model-calls", 0, "required Router experiment model-call budget")
	routeTokens := flags.Int("route-max-total-tokens", 0, "required Router experiment token budget")
	embeddingCalls := flags.Int("max-embedding-calls", 0, "required RAG physical embedding request budget")
	embeddingTokens := flags.Int("max-embedding-input-tokens", 0, "required RAG estimated input-token budget")
	timeout := flags.Duration("timeout", 60*time.Second, "deadline per chat sample, at most 5m")
	embeddingTimeout := flags.Duration("embedding-timeout", 2*time.Minute, "deadline for the embedding evaluation, at most 5m")
	minSimilarity := flags.Float64("min-similarity", 0.15, "calibrated dense similarity threshold")
	minEvidenceCoverage := flags.Float64("min-evidence-coverage", 0.25, "calibrated evidence coverage threshold")
	outputDir := flags.String("output-dir", "../../.cache/live-benchmark", "evidence pack directory")
	suitePath := flags.String("suite", "../../examples/benchmark-suite.v1.json", "CASE-001 benchmark manifest")
	ragDataset := flags.String("rag-dataset", "../../examples/knowledge/golden-dataset.v1.json", "RAG benchmark dataset")
	corpusManifest := flags.String("corpus-manifest", "../../examples/knowledge/golden-v1/corpus-manifest.v1.json", "RAG corpus manifest")
	routeDataset := flags.String("route-dataset", "../../examples/routing/golden-dataset.v1.json", "Router benchmark dataset")
	enforce := flags.Bool("enforce", false, "exit 1 unless the evidence completeness gate passes")
	if flags.Parse(args) != nil || flags.NArg() != 0 {
		return 2
	}
	if !*live || !credential.FromEnvironment("OPENAI_API_KEY").Available() || strings.TrimSpace(*model) == "" || strings.TrimSpace(*embeddingModel) == "" ||
		*embeddingDimensions < 1 || *trials < 3 || *trials > 20 || *toolCalls < 1 || *toolTokens < 1 || *routeCalls < 1 || *routeTokens < 1 ||
		*embeddingCalls < 1 || *embeddingTokens < 1 || *timeout <= 0 || *timeout > 5*time.Minute || *embeddingTimeout <= 0 || *embeddingTimeout > 5*time.Minute || strings.TrimSpace(*outputDir) == "" {
		fmt.Fprintln(stderr, "Live benchmark requires --live, OPENAI_API_KEY, both models, dimensions, 3-20 trials, explicit positive Tool/Router/Embedding budgets, valid timeouts, and --output-dir")
		return 2
	}

	startedAt := time.Now().UTC()
	suiteBytes, suite, taskCount, err := loadBenchmarkSuite(*suitePath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if taskCount < 8 || taskCount > 12 {
		fmt.Fprintf(stderr, "CASE-001A requires 8-12 frozen benchmark tasks; manifest contains %d\n", taskCount)
		return 2
	}
	if err := os.MkdirAll(*outputDir, 0700); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if err := os.WriteFile(filepath.Join(*outputDir, "benchmark-suite.v1.json"), suiteBytes, 0600); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}

	type captured struct {
		artifact liveBenchmarkArtifact
		content  []byte
	}
	capture := func(name, kind string, live bool, run func(io.Writer, io.Writer) int) (captured, error) {
		var reportOut, reportErr bytes.Buffer
		if code := run(&reportOut, &reportErr); code != 0 {
			return captured{}, fmt.Errorf("%s failed (exit %d): %s", name, code, strings.TrimSpace(reportErr.String()))
		}
		if reportErr.Len() > 0 {
			fmt.Fprintf(stderr, "%s: %s", name, reportErr.String())
		}
		file := name + ".json"
		content := reportOut.Bytes()
		if err := os.WriteFile(filepath.Join(*outputDir, file), content, 0600); err != nil {
			return captured{}, err
		}
		return captured{artifact: liveBenchmarkArtifact{Name: name, File: file, EvaluationKind: kind, Live: live, SHA256: benchmarkDigest(content)}, content: content}, nil
	}

	ragArgs := []string{"--dataset", *ragDataset, "--corpus-manifest", *corpusManifest, "--retrieval-mode", "hybrid",
		"--embedding-profile", *embeddingProfile, "--live-embeddings", "--embedding-base-url", *embeddingBaseURL,
		"--embedding-model", *embeddingModel, "--embedding-dimensions", fmt.Sprint(*embeddingDimensions),
		"--max-embedding-calls", fmt.Sprint(*embeddingCalls), "--max-embedding-input-tokens", fmt.Sprint(*embeddingTokens),
		"--embedding-retry-attempts", "1", "--embedding-timeout", embeddingTimeout.String(),
		"--min-similarity", fmt.Sprint(*minSimilarity), "--min-evidence-coverage", fmt.Sprint(*minEvidenceCoverage)}
	toolArgs := []string{"--live", "--model", *model, "--base-url", *baseURL, "--trials", fmt.Sprint(*trials),
		"--max-model-calls", fmt.Sprint(*toolCalls), "--max-total-tokens", fmt.Sprint(*toolTokens), "--timeout", timeout.String()}
	routeQueryArgs := []string{"--dataset", *routeDataset}
	routeLiveArgs := []string{"--dataset", *routeDataset, "--live", "--model", *model, "--base-url", *baseURL,
		"--trials", fmt.Sprint(*trials), "--max-model-calls", fmt.Sprint(*routeCalls),
		"--max-total-tokens", fmt.Sprint(*routeTokens), "--timeout", timeout.String()}

	runs := []struct {
		name, kind string
		live       bool
		call       func(io.Writer, io.Writer) int
	}{
		{"rag-semantic", "rag_retrieval", true, func(o, e io.Writer) int { return runRAG(ctx, ragArgs, o, e) }},
		{"tool-context-vs-tools", "live_model", true, func(o, e io.Writer) int { return runTool(ctx, toolArgs, o, e) }},
		{"route-query-match", "agent_routing", false, func(o, e io.Writer) int { return runRoute(ctx, routeQueryArgs, o, e) }},
		{"route-llm-ranking", "agent_routing", true, func(o, e io.Writer) int { return runRoute(ctx, routeLiveArgs, o, e) }},
	}
	capturedReports := make([]captured, 0, len(runs))
	for _, item := range runs {
		report, err := capture(item.name, item.kind, item.live, item.call)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
		capturedReports = append(capturedReports, report)
	}

	var ragReport rageval.Report
	var toolReport tooleval.Report
	var queryReport, llmReport routeeval.Report
	if json.Unmarshal(capturedReports[0].content, &ragReport) != nil || json.Unmarshal(capturedReports[1].content, &toolReport) != nil ||
		json.Unmarshal(capturedReports[2].content, &queryReport) != nil || json.Unmarshal(capturedReports[3].content, &llmReport) != nil {
		fmt.Fprintln(stderr, "generated benchmark report could not be decoded")
		return 2
	}
	toolComparison := compareToolArms(toolReport.Samples)
	routerComparison := compareRouterModes(queryReport.Samples, llmReport.Samples)
	failedOrSkipped := countBenchmarkFailures(ragReport.Samples, toolReport.Samples, llmReport.Samples)
	noGain := toolComparison.Unchanged+toolComparison.Regressed+routerComparison.Unchanged+routerComparison.Regressed > 0
	gate := evalreport.Gate{Passed: len(capturedReports) == 4 && len(suite.FailureScenarios) > 0 && noGain,
		BlockingSamples: 2, Reasons: []string{}}
	if len(suite.FailureScenarios) == 0 {
		gate.BlockingFailures++
		gate.Reasons = append(gate.Reasons, "no declared failure path is traceable from the benchmark manifest")
	}
	if !noGain {
		gate.BlockingFailures++
		gate.Reasons = append(gate.Reasons, "no evaluated no-gain or regression pair was retained")
	}
	manifest := liveBenchmarkManifest{
		Identity: evalreport.Identity{ReportFormat: evalreport.Format, EvaluationKind: "live_model_benchmark", DatasetID: suite.ID,
			DatasetVersion: suite.Version, DatasetHash: benchmarkDigest(suiteBytes), GitRevision: gitRevision(ctx), StartedAt: startedAt, CompletedAt: time.Now().UTC()},
		SchemaVersion: liveBenchmarkSchemaVersion,
		Config: liveBenchmarkConfig{ChatModel: *model, EmbeddingProfile: *embeddingProfile, EmbeddingModel: *embeddingModel,
			EmbeddingDimensions: *embeddingDimensions, Trials: *trials, ToolMaxModelCalls: *toolCalls, ToolMaxTotalTokens: *toolTokens,
			RouteMaxModelCalls: *routeCalls, RouteMaxTotalTokens: *routeTokens, MaxEmbeddingCalls: *embeddingCalls,
			MaxEmbeddingInputTokens: *embeddingTokens, SampleTimeout: *timeout, EmbeddingTimeout: *embeddingTimeout},
		Artifacts: make([]liveBenchmarkArtifact, 0, len(capturedReports)),
		Evidence: liveBenchmarkEvidence{BenchmarkTasks: taskCount, DeclaredFailureScenarios: len(suite.FailureScenarios),
			ObservedFailedOrSkipped: failedOrSkipped, ToolContextComparison: toolComparison, RouterModeComparison: routerComparison,
			NoGainOrRegressionObserved: noGain},
		CostSource: "per-report usage only; monetary cost remains unavailable when the provider has no price table", Gate: gate,
	}
	for _, report := range capturedReports {
		manifest.Artifacts = append(manifest.Artifacts, report.artifact)
	}
	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	manifestBytes = append(manifestBytes, '\n')
	if err := os.WriteFile(filepath.Join(*outputDir, "manifest.json"), manifestBytes, 0600); err != nil || !writeJSON(out, stderr, manifest) {
		if err != nil {
			fmt.Fprintln(stderr, err)
		}
		return 2
	}
	fmt.Fprintf(stderr, "CASE-001A: tasks=%d reports=%d failures_or_skipped=%d no_gain_or_regression=%t output=%s\n",
		taskCount, len(capturedReports), failedOrSkipped, noGain, *outputDir)
	if *enforce && !gate.Passed {
		return 1
	}
	return 0
}

func loadBenchmarkSuite(path string) ([]byte, benchmarkSuiteIdentity, int, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, benchmarkSuiteIdentity{}, 0, fmt.Errorf("read benchmark manifest: %w", err)
	}
	var suite benchmarkSuiteIdentity
	if json.Unmarshal(content, &suite) != nil || suite.SchemaVersion != "agentflow-benchmark-suite-v1" || strings.TrimSpace(suite.ID) == "" || strings.TrimSpace(suite.Version) == "" {
		return nil, benchmarkSuiteIdentity{}, 0, errors.New("invalid agentflow-benchmark-suite-v1 manifest")
	}
	tasks := 0
	for _, set := range suite.TaskSets {
		tasks += len(set.Cases)
	}
	return content, suite, tasks, nil
}

func compareToolArms(samples []tooleval.Sample) binaryComparison {
	baseline := map[string]tooleval.Sample{}
	for _, sample := range samples {
		if sample.Arm == "full_context" {
			baseline[fmt.Sprintf("%s/%d", sample.TaskID, sample.Trial)] = sample
		}
	}
	var result binaryComparison
	for _, sample := range samples {
		if sample.Arm != "with_tools" {
			continue
		}
		result.Pairs++
		base, ok := baseline[fmt.Sprintf("%s/%d", sample.TaskID, sample.Trial)]
		if !ok || base.Status == "not_evaluated" || sample.Status == "not_evaluated" {
			result.NotEvaluated++
		} else if !base.Verified && sample.Verified {
			result.Improved++
		} else if base.Verified && !sample.Verified {
			result.Regressed++
		} else {
			result.Unchanged++
		}
	}
	return result
}

func compareRouterModes(baseline, live []routeeval.Sample) binaryComparison {
	byCase := map[string]routeeval.Sample{}
	for _, sample := range baseline {
		byCase[sample.CaseID] = sample
	}
	var result binaryComparison
	for _, sample := range live {
		result.Pairs++
		base, ok := byCase[sample.CaseID]
		if !ok || base.Status != "completed" || sample.Status != "completed" {
			result.NotEvaluated++
			continue
		}
		baseCorrect, liveCorrect := routeSampleCorrect(base), routeSampleCorrect(sample)
		if !baseCorrect && liveCorrect {
			result.Improved++
		} else if baseCorrect && !liveCorrect {
			result.Regressed++
		} else {
			result.Unchanged++
		}
	}
	return result
}

func routeSampleCorrect(sample routeeval.Sample) bool {
	return sample.ExpectedOutcome == sample.ActualOutcome &&
		(sample.ExpectedOutcome != agent.AgentSelectionOutcomeSelected || sample.AcceptableSelection) && !sample.UnsafeFalseRoute
}

func countBenchmarkFailures(ragSamples []rageval.Sample, toolSamples []tooleval.Sample, routeSamples []routeeval.Sample) int {
	count := 0
	for _, sample := range ragSamples {
		if sample.Status != "completed" {
			count++
		}
	}
	for _, sample := range toolSamples {
		if sample.Status != "completed" {
			count++
		}
	}
	for _, sample := range routeSamples {
		if sample.Status != "completed" {
			count++
		}
	}
	return count
}

func benchmarkDigest(content []byte) string {
	sum := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(sum[:])
}
