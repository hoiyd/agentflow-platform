package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"time"

	"agentflow-platform/apps/api/internal/evaluation/runtimehost"
	"agentflow-platform/apps/api/internal/evaluation/tooleval"
)

type fileSkillCLI struct {
	database, dataset, skills, credential, evidence *string
	sampleCalls, sampleTokens, extraRequests        *int
	suiteTimeout                                    *time.Duration
	tokenRatio                                      *float64
}

func fileSkillFlags(f *flag.FlagSet) fileSkillCLI {
	return fileSkillCLI{
		database:      f.String("database-url", "", "dedicated Postgres test database URL with CREATEDB privileges"),
		dataset:       f.String("dataset", "", "versioned file-skill-task-v1 JSON dataset"),
		skills:        f.String("skill-dir", "", "trusted directory containing the synthetic method package"),
		credential:    f.String("credential-env", "OPENAI_API_KEY", "environment variable containing the model credential"),
		evidence:      f.String("evidence-mode", "live_model", "live_model or deterministic_http_fixture"),
		sampleCalls:   f.Int("sample-max-model-calls", 12, "per-Run logical model-call ceiling"),
		sampleTokens:  f.Int("sample-max-tokens", 60000, "per-Run reserved and consumed token ceiling"),
		extraRequests: f.Int("max-extra-model-requests", 3, "candidate allowed physical requests above its baseline"),
		suiteTimeout:  f.Duration("suite-timeout", 5*time.Minute, "total file/Skill evaluation deadline, at most 10m"),
		tokenRatio:    f.Float64("max-token-ratio", 2, "candidate/baseline token ratio allowed by rollout gate"),
	}
}

func runFileSkill(ctx context.Context, out, stderr io.Writer, f fileSkillCLI, live bool, model, base string, trials, calls, tokens int, timeout time.Duration, enforce bool, positional int) int {
	if !live || positional != 0 {
		fmt.Fprintln(stderr, "file_skill requires --live and explicit target, database, dataset, Skill directory and positive suite budgets")
		return 2
	}
	report, err := tooleval.RunFileSkill(ctx, tooleval.FileSkillOptions{
		Options: runtimehost.Options{DatabaseURL: *f.database, BaseURL: base, Model: model,
			CredentialEnvironment: *f.credential, MaxRequests: calls, SampleCalls: *f.sampleCalls,
			SampleTokens: *f.sampleTokens, TimeoutMS: timeout.Milliseconds()},
		DatasetPath: *f.dataset, SkillDirectory: *f.skills, Revision: gitRevision(ctx),
		EvidenceMode: *f.evidence, Trials: trials, MaxTokens: tokens,
		SuiteTimeoutMS: f.suiteTimeout.Milliseconds(), MaxTokenRatio: *f.tokenRatio, MaxExtraRequests: *f.extraRequests,
	})
	// Retain partial evidence on infrastructure/cleanup failure, but never mark it
	// as a passing experiment or silently retry the whole suite.
	if report.SchemaVersion != "" && !writeJSON(out, stderr, report) {
		return 2
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	fmt.Fprintf(stderr, "file_skill: samples=%d physical_requests=%d tokens=%d gate=%t\n", len(report.Samples), report.PhysicalRequests, report.TotalTokens, report.Gate.Passed)
	if enforce && !report.Gate.Passed {
		return 1
	}
	return 0
}
