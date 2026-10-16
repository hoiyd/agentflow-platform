package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/credential"
	"agentflow-platform/apps/api/internal/evaluation/contexteval"
	"agentflow-platform/apps/api/internal/inference/openai"
)

func runLiveContext(ctx context.Context, args []string, out, stderr io.Writer) int {
	flags := flag.NewFlagSet("eval context-live", flag.ContinueOnError)
	flags.SetOutput(stderr)
	live := flags.Bool("live", false, "explicitly authorize summary, fact probe and continuation model calls")
	dataset := flags.String("dataset", "../../examples/context/compaction-continuation.v1.json", "versioned synthetic task dataset")
	model := flags.String("model", "", "required OpenAI-compatible model ID")
	base := flags.String("base-url", "https://api.openai.com/v1", "OpenAI-compatible base URL (not stored in report)")
	trials := flags.Int("trials", 3, "trials per task, rotating two arms (1-20)")
	calls := flags.Int("max-model-calls", 0, "required shared suite-wide physical model-call budget")
	tokens := flags.Int("max-total-tokens", 0, "required shared suite-wide summary/probe/continuation token budget")
	timeout := flags.Duration("timeout", 60*time.Second, "deadline per arm including summary, probes and Tools; at most 5m")
	window := flags.Int("context-window-tokens", 32768, "same context window for both arms; must match the target deployment")
	summary := flags.Int("summary-max-tokens", 1024, "production Compactor summary cap")
	enforce := flags.Bool("enforce", false, "exit 1 if candidate constraints or pair completeness fail")
	if flags.Parse(args) != nil || flags.NArg() != 0 {
		return 2
	}
	key := credential.FromEnvironment("OPENAI_API_KEY")
	if !*live || !key.Available() || strings.TrimSpace(*model) == "" || *calls < 1 || *tokens < 1 || *window <= 0 || *summary <= 0 {
		fmt.Fprintln(stderr, "context-live requires --live, OPENAI_API_KEY, --model, positive --max-model-calls/--max-total-tokens and context/summary limits")
		return 2
	}
	client := openai.NewClientWithTimeout(key.Reveal(), *base, *model, *timeout)
	report, err := contexteval.RunLive(ctx, client, contexteval.LiveOptions{DatasetPath: *dataset, Trials: *trials, MaxModelCalls: *calls, MaxTotalTokens: *tokens,
		Timeout: *timeout, ContextWindowTokens: *window, SummaryMaxTokens: *summary, Revision: gitRevision(ctx), EvidenceMode: "live_model"})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if !writeJSON(out, stderr, report) {
		return 2
	}
	fmt.Fprintf(stderr, "context-live: verified=%d/%d comparable=%d/%d improved=%d regressed=%d calls=%d tokens=%d passed=%t\n",
		report.Summary.Verified, report.Summary.Samples, report.Summary.Comparable, report.Summary.Pairs, report.Summary.Improved, report.Summary.Regressed, report.Usage.ModelCalls, report.Usage.TotalTokens, report.Gate.Passed)
	if *enforce && !report.Gate.Passed {
		return 1
	}
	return 0
}
