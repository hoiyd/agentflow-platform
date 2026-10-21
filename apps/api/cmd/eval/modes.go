package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"time"

	"agentflow-platform/apps/api/internal/evaluation/modeeval"
)

func runModes(ctx context.Context, args []string, out, stderr io.Writer) int {
	flags := flag.NewFlagSet("eval modes", flag.ContinueOnError)
	flags.SetOutput(stderr)
	live := flags.Bool("live", false, "explicitly authorize physical model requests")
	dataset := flags.String("dataset", "../../examples/orchestration/report-task.v1.json", "versioned sourced-report task")
	database := flags.String("database-url", "", "dedicated Postgres test server with CREATEDB; evaluator creates/drops a fresh database")
	base := flags.String("base-url", "", "OpenAI-compatible Tool-capable endpoint")
	model := flags.String("model", "", "required model ID")
	credential := flags.String("credential-env", "OPENAI_API_KEY", "credential environment variable name; never its value")
	evidence := flags.String("evidence-mode", "live_model", "live_model or deterministic_http_fixture")
	trials := flags.Int("trials", 3, "repetitions per mode; order rotates (1-20)")
	requests := flags.Int("max-model-requests", 0, "required suite-wide physical model request ceiling")
	tokens := flags.Int("max-total-tokens", 0, "required suite-wide token ceiling")
	calls := flags.Int("sample-max-model-calls", 0, "same logical model-call ceiling for every Run")
	sampleTokens := flags.Int("sample-max-tokens", 0, "same token ceiling reserved for every Run")
	timeout := flags.Duration("timeout", time.Minute, "wall deadline per arm, at most 5m")
	suiteTimeout := flags.Duration("suite-timeout", 5*time.Minute, "total suite deadline, at most 10m")
	approval := flags.String("approval-policy", "approve", "fixed Multi plan policy: approve, edit or reject")
	enforce := flags.Bool("enforce", false, "exit 1 on incomplete arms or common-contract drift; quality remains an independent measurement")
	if flags.Parse(args) != nil || flags.NArg() != 0 {
		return 2
	}
	if !*live {
		fmt.Fprintln(stderr, "eval modes requires --live and explicit target, database and shared/per-Run budgets")
		return 2
	}
	report, err := modeeval.Run(ctx, modeeval.Options{DatasetPath: *dataset, DatabaseURL: *database, BaseURL: *base, Model: *model, CredentialEnvironment: *credential, EvidenceMode: *evidence, Trials: *trials, MaxRequests: *requests, MaxTokens: *tokens, SampleCalls: *calls, SampleTokens: *sampleTokens, TimeoutMS: timeout.Milliseconds(), SuiteTimeoutMS: suiteTimeout.Milliseconds(), ApprovalPolicy: *approval, Revision: gitRevision(ctx)})
	if err != nil {
		if report.SchemaVersion != "" {
			_ = writeJSON(out, stderr, report)
		}
		fmt.Fprintln(stderr, err)
		return 2
	}
	if !writeJSON(out, stderr, report) {
		return 2
	}
	fmt.Fprintf(stderr, "modes: samples=%d physical_requests=%d total_tokens=%d evidence_gate=%t\n", len(report.Samples), report.PhysicalRequests, report.TotalTokens, report.Gate.Passed)
	if *enforce && !report.Gate.Passed {
		return 1
	}
	return 0
}
