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
		fmt.Fprintln(stderr, "usage: eval <rag|tool> [options]")
		return 2
	}
	switch args[0] {
	case "rag":
		return runRAG(ctx, args[1:], out, stderr)
	case "tool":
		return runTool(ctx, args[1:], out, stderr)
	default:
		fmt.Fprintf(stderr, "unknown evaluation suite %q; use rag or tool\n", args[0])
		return 2
	}
}

func runRAG(ctx context.Context, args []string, out, stderr io.Writer) int {
	flags := flag.NewFlagSet("eval rag", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dataset := flags.String("dataset", "../../examples/knowledge/golden-dataset.v1.json", "Golden Dataset JSON path")
	manifest := flags.String("corpus-manifest", "../../examples/knowledge/golden-v1/corpus-manifest.v1.json", "corpus manifest JSON path")
	topK := flags.Int("top-k", 5, "retrieval result limit (1-20)")
	minSimilarity := flags.Float64("min-similarity", 0.15, "minimum dense similarity (0-1)")
	baselinePath := flags.String("baseline", "", "optional prior rag-eval-v1 JSON report")
	ablation := flags.Bool("ablation", false, "allow exactly one declared pipeline/configuration difference")
	enforce := flags.Bool("enforce", false, "exit 1 when the gate or comparable baseline check fails")
	if flags.Parse(args) != nil || flags.NArg() != 0 {
		return 2
	}
	if *ablation && *baselinePath == "" {
		fmt.Fprintln(stderr, "--ablation requires --baseline")
		return 2
	}
	report, err := rageval.Run(ctx, rageval.Options{DatasetPath: *dataset, CorpusManifestPath: *manifest,
		TopK: *topK, MinSimilarity: *minSimilarity, Revision: gitRevision(ctx)})
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
	fmt.Fprintf(stderr, "rag: passed=%d/%d evaluated=%d gating_failures=%d mrr=%.3f ndcg=%.3f leaks=%d latency_ms=%.1f\n",
		report.Summary.Passed, report.Summary.Samples, report.Summary.Evaluated, report.Summary.GatingFailures,
		report.Summary.MRR, report.Summary.NDCG, report.Summary.LeakCount, report.Summary.MeanLatencyMS)
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
