package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/contexteval"
	"agentflow-platform/apps/api/internal/openai"
	"agentflow-platform/apps/api/internal/rageval"
	"agentflow-platform/apps/api/internal/tooleval"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, out, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: eval <context|rag|tool> [options]")
		return 2
	}
	switch args[0] {
	case "context":
		return runContext(ctx, args[1:], out, stderr)
	case "rag":
		return runRAG(ctx, args[1:], out, stderr)
	case "tool":
		return runTool(ctx, args[1:], out, stderr)
	default:
		fmt.Fprintf(stderr, "unknown evaluation suite %q; use context, rag, or tool\n", args[0])
		return 2
	}
}

func runContext(ctx context.Context, args []string, out, stderr io.Writer) int {
	flags := flag.NewFlagSet("eval context", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dataset := flags.String("dataset", "../../examples/context/golden-dataset.v1.json", "Context quality dataset JSON path")
	strategy := flags.String("strategy", contexteval.StrategyCompacted, "context strategy: compacted_history or full_history")
	baselinePath := flags.String("baseline", "", "optional context-quality-eval-v1 JSON report")
	ablation := flags.Bool("ablation", false, "allow one explicit strategy difference")
	enforce := flags.Bool("enforce", false, "exit 1 when the gate or comparable baseline check fails")
	if flags.Parse(args) != nil || flags.NArg() != 0 {
		return 2
	}
	if *ablation && *baselinePath == "" {
		fmt.Fprintln(stderr, "--ablation requires --baseline")
		return 2
	}
	report, err := contexteval.Run(ctx, contexteval.Options{DatasetPath: *dataset, Strategy: *strategy, Revision: gitRevision(ctx)})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if *baselinePath != "" {
		var baseline contexteval.Report
		content, err := os.ReadFile(*baselinePath)
		if err != nil || json.Unmarshal(content, &baseline) != nil {
			fmt.Fprintln(stderr, "baseline must be a readable context-quality-eval-v1 JSON report")
			return 2
		}
		contexteval.ApplyComparison(&report, baseline, *ablation)
	}
	if !writeJSON(out, stderr, report) {
		return 2
	}
	fmt.Fprintf(stderr, "context: strategy=%s passed=%d/%d evaluated=%d gating_failures=%d retention=%.3f leaks=%d tokens=%.1f irrelevant=%.3f\n",
		report.Config.Strategy, report.Summary.Passed, report.Summary.Samples, report.Summary.Evaluated,
		report.Summary.GatingFailures, report.Summary.RequiredFactRetention, report.Summary.ForbiddenLeaks,
		report.Summary.MeanInputTokens, report.Summary.MeanIrrelevantRatio)
	if *enforce && !report.Gate.Passed {
		return 1
	}
	return 0
}

func runRAG(ctx context.Context, args []string, out, stderr io.Writer) int {
	flags := flag.NewFlagSet("eval rag", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dataset := flags.String("dataset", "../../examples/knowledge/golden-dataset.v1.json", "Golden Dataset JSON path")
	manifest := flags.String("corpus-manifest", "../../examples/knowledge/golden-v1/corpus-manifest.v1.json", "corpus manifest JSON path")
	topK := flags.Int("top-k", 5, "retrieval result limit (1-20)")
	minSimilarity := flags.Float64("min-similarity", 0.15, "minimum dense similarity (0-1)")
	minimumEvidenceCoverage := flags.Float64("min-evidence-coverage", 0.25, "minimum query-term coverage required by the relevance gate (0.05-1)")
	recallArm := flags.String("recall-arm", "hybrid", "recall path: dense_only, lexical_only, or hybrid")
	embeddingProfile := flags.String("embedding-profile", "hash", "embedding profile: hash, openai_compatible, or ollama")
	liveEmbeddings := flags.Bool("live-embeddings", false, "explicitly authorize embedding model requests")
	embeddingBaseURL := flags.String("embedding-base-url", "https://api.openai.com/v1", "OpenAI-compatible base URL or Ollama /api/embed URL")
	embeddingModel := flags.String("embedding-model", "", "required model ID for a real embedding profile")
	embeddingDimensions := flags.Int("embedding-dimensions", 0, "required vector dimensions for a real embedding profile")
	maxEmbeddingCalls := flags.Int("max-embedding-calls", 0, "required physical request budget across indexing, queries, and retries")
	maxEmbeddingInputTokens := flags.Int("max-embedding-input-tokens", 0, "required estimated input-token budget across indexing and queries")
	embeddingRetryAttempts := flags.Int("embedding-retry-attempts", 2, "maximum attempts per embedding input (1-5)")
	embeddingTimeout := flags.Duration("embedding-timeout", 2*time.Minute, "deadline for the complete embedding evaluation, at most 5m")
	baselinePath := flags.String("baseline", "", "optional prior rag-eval-v1 JSON report")
	ablation := flags.Bool("ablation", false, "allow exactly one declared pipeline/configuration difference")
	enforce := flags.Bool("enforce", false, "exit 1 when the gate or comparable baseline check fails")
	if flags.Parse(args) != nil || flags.NArg() != 0 {
		return 2
	}
	provided := map[string]bool{}
	flags.Visit(func(item *flag.Flag) { provided[item.Name] = true })
	if !strings.EqualFold(strings.TrimSpace(*embeddingProfile), rageval.EmbeddingProfileHash) && (!provided["min-similarity"] || !provided["min-evidence-coverage"]) {
		fmt.Fprintln(stderr, "real embedding profiles require explicit --min-similarity and --min-evidence-coverage values; calibrate them before interpreting quality")
		return 2
	}
	if *ablation && *baselinePath == "" {
		fmt.Fprintln(stderr, "--ablation requires --baseline")
		return 2
	}
	report, err := rageval.Run(ctx, rageval.Options{DatasetPath: *dataset, CorpusManifestPath: *manifest,
		TopK: *topK, MinSimilarity: *minSimilarity, MinimumEvidenceCoverage: *minimumEvidenceCoverage, RecallArm: *recallArm,
		EmbeddingProfile: rageval.EmbeddingProfileOptions{Name: *embeddingProfile, Live: *liveEmbeddings, APIKey: os.Getenv("OPENAI_API_KEY"),
			BaseURL: *embeddingBaseURL, Model: *embeddingModel, Dimensions: *embeddingDimensions, MaxCalls: *maxEmbeddingCalls,
			MaxInputTokens: *maxEmbeddingInputTokens, RetryMaxAttempts: *embeddingRetryAttempts, Timeout: *embeddingTimeout},
		Revision: gitRevision(ctx)})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if *baselinePath != "" {
		var baseline rageval.Report
		content, err := os.ReadFile(*baselinePath)
		if err != nil || json.Unmarshal(content, &baseline) != nil {
			fmt.Fprintln(stderr, "baseline must be a readable rag-eval-v1 JSON report")
			return 2
		}
		rageval.ApplyComparison(&report, baseline, *ablation)
	}
	if !writeJSON(out, stderr, report) {
		return 2
	}
	fmt.Fprintf(stderr, "rag: profile=%s arm=%s passed=%d/%d evaluated=%d gating_failures=%d mrr=%.3f ndcg=%.3f leaks=%d latency_ms=%.1f embedding_requests=%d\n",
		report.EmbeddingProfile.Name, report.Config.RecallArm,
		report.Summary.Passed, report.Summary.Samples, report.Summary.Evaluated, report.Summary.GatingFailures,
		report.Summary.MRR, report.Summary.NDCG, report.Summary.LeakCount, report.Summary.MeanLatencyMS, report.EmbeddingProfile.Usage.PhysicalRequests)
	if *enforce && !report.Gate.Passed {
		return 1
	}
	return 0
}

func runTool(ctx context.Context, args []string, out, stderr io.Writer) int {
	flags := flag.NewFlagSet("eval tool", flag.ContinueOnError)
	flags.SetOutput(stderr)
	live := flags.Bool("live", false, "explicitly authorize model API requests")
	model := flags.String("model", "", "required model ID")
	base := flags.String("base-url", "https://api.openai.com/v1", "OpenAI-compatible base URL")
	trials := flags.Int("trials", 1, "repetitions per task and arm (1-20)")
	calls := flags.Int("max-model-calls", 0, "required total suite model-call budget")
	tokens := flags.Int("max-total-tokens", 0, "required total suite token budget")
	timeout := flags.Duration("timeout", 60*time.Second, "deadline per sample, at most 5m")
	enforce := flags.Bool("enforce", false, "exit 1 if any Tool-arm task fails verification")
	if flags.Parse(args) != nil {
		return 2
	}
	if !*live || strings.TrimSpace(*model) == "" || os.Getenv("OPENAI_API_KEY") == "" || flags.NArg() != 0 {
		fmt.Fprintln(stderr, "Requires --live, --model, OPENAI_API_KEY and explicit positive budgets. Offline: go test ./internal/tooleval")
		return 2
	}
	client := openai.NewClientWithTimeout(os.Getenv("OPENAI_API_KEY"), *base, *model, *timeout)
	report, err := tooleval.Run(ctx, client, tooleval.Options{Trials: *trials, MaxModelCalls: *calls,
		MaxTotalTokens: *tokens, Timeout: *timeout, Revision: gitRevision(ctx)})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if !writeJSON(out, stderr, report) {
		return 2
	}
	for _, arm := range []string{"without_tools", "with_tools"} {
		summary := report.Summary[arm]
		fmt.Fprintf(stderr, "%s: verified=%d/%d evaluated=%d tokens=%.1f model_calls=%.1f tool_calls=%.1f latency_ms=%.1f\n",
			arm, summary.Verified, summary.Samples, summary.Evaluated, summary.MeanTokens, summary.MeanModelCalls,
			summary.MeanToolCalls, summary.MeanLatencyMS)
	}
	if *enforce && !report.Passed() {
		return 1
	}
	return 0
}

func writeJSON(out, stderr io.Writer, value any) bool {
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		fmt.Fprintln(stderr, err)
		return false
	}
	return true
}

func gitRevision(ctx context.Context) string {
	value, err := exec.CommandContext(ctx, "git", "rev-parse", "HEAD").Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(value))
}
