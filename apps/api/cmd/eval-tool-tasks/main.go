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
	"agentflow-platform/apps/api/internal/tooleval"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, out, stderr io.Writer) int {
	flags := flag.NewFlagSet("eval-tool-tasks", flag.ContinueOnError)
	flags.SetOutput(stderr)
	live := flags.Bool("live", false, "explicitly authorize model API requests")
	model := flags.String("model", "", "required model ID")
	base := flags.String("base-url", "https://api.openai.com/v1", "OpenAI-compatible base URL")
	trials := flags.Int("trials", 1, "repetitions per task and arm (1-20)")
	calls := flags.Int("max-model-calls", 0, "required total suite model-call budget")
	tokens := flags.Int("max-total-tokens", 0, "required total suite token budget (estimates when usage is absent)")
	timeout := flags.Duration("timeout", 60*time.Second, "deadline per sample, at most 5m")
	enforce := flags.Bool("enforce", false, "exit 1 if any Tool-arm task fails verification")
	if flags.Parse(args) != nil {
		return 2
	}
	if !*live || strings.TrimSpace(*model) == "" || os.Getenv("OPENAI_API_KEY") == "" || flags.NArg() != 0 {
		fmt.Fprintln(stderr, "Requires --live, --model, OPENAI_API_KEY and explicit positive budgets. Offline: go test ./internal/tooleval")
		return 2
	}
	revision := "unknown"
	if value, err := exec.CommandContext(ctx, "git", "rev-parse", "HEAD").Output(); err == nil {
		revision = strings.TrimSpace(string(value))
	}
	client := openai.NewClientWithTimeout(os.Getenv("OPENAI_API_KEY"), *base, *model, *timeout)
	report, err := tooleval.Run(ctx, client, tooleval.Options{Trials: *trials, MaxModelCalls: *calls, MaxTotalTokens: *tokens, Timeout: *timeout, Revision: revision})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	for _, arm := range []string{"without_tools", "with_tools"} {
		summary := report.Summary[arm]
		fmt.Fprintf(stderr, "%s: verified=%d/%d evaluated=%d tokens=%.1f model_calls=%.1f tool_calls=%.1f latency_ms=%.1f\n", arm, summary.Verified, summary.Samples, summary.Evaluated, summary.MeanTokens, summary.MeanModelCalls, summary.MeanToolCalls, summary.MeanLatencyMS)
	}
	if *enforce && !report.Passed() {
		return 1
	}
	return 0
}
