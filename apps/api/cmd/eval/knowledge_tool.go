package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"time"

	"agentflow-platform/apps/api/internal/evaluation/evalreport"
	"agentflow-platform/apps/api/internal/evaluation/runtimehost"
	"agentflow-platform/apps/api/internal/evaluation/tooleval"
)

func runKnowledgeTool(ctx context.Context, out, stderr io.Writer, flags *flag.FlagSet, f toolTaskCLI, live bool, model, base string, trials, calls, tokens int, timeout time.Duration, enforce bool) int {
	misplaced := false
	flags.Visit(func(option *flag.Flag) {
		switch option.Name {
		case "dataset", "skill-dir", "max-token-ratio", "max-extra-model-requests":
			misplaced = true
		}
	})
	if misplaced || !live || flags.NArg() != 0 {
		fmt.Fprintln(stderr, "knowledge requires --live and does not accept file/Skill experiment flags")
		return 2
	}
	report, err := tooleval.RunKnowledge(ctx, tooleval.KnowledgeOptions{
		Options: runtimehost.Options{DatabaseURL: *f.database, BaseURL: base, Model: model,
			CredentialEnvironment: *f.credential, MaxRequests: calls, SampleCalls: *f.sampleCalls,
			SampleTokens: *f.sampleTokens, TimeoutMS: timeout.Milliseconds()},
		Revision: evalreport.GitRevision(ctx), EvidenceMode: *f.evidence, Trials: trials,
		MaxTokens: tokens, SuiteTimeoutMS: f.suiteTimeout.Milliseconds(),
	})
	if report.SchemaVersion != "" && !writeJSON(out, stderr, report) {
		return 2
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	fmt.Fprintf(stderr, "knowledge: verified=%d/%d physical_requests=%d tokens=%d gate=%t\n",
		report.Verified, len(report.Samples), report.PhysicalRequests, report.TotalTokens, report.Gate.Passed)
	if enforce && !report.Gate.Passed {
		return 1
	}
	return 0
}
