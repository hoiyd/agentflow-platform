package modeeval

import (
	"context"
	"errors"
	"net/url"
	"slices"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/credential"
	"agentflow-platform/apps/api/internal/evaluation/evalreport"
)

// Run has explicit live authorization at the CLI. It always uses a fresh
// database and the canonical app composition, never an operator's existing data.
func Run(ctx context.Context, opts Options) (report Report, err error) {
	u, e := url.Parse(opts.BaseURL)
	if e != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") ||
		u.User != nil || u.RawQuery != "" || u.Fragment != "" ||
		strings.TrimSpace(opts.Model) == "" || strings.ContainsAny(opts.Model, "\r\n") ||
		!credential.FromEnvironment(opts.CredentialEnvironment).Available() ||
		opts.Trials < 1 || opts.Trials > 20 || opts.MaxRequests < 1 || opts.MaxTokens < 1 ||
		opts.SampleCalls < 1 || opts.SampleTokens < 1 || opts.SampleTokens > opts.MaxTokens ||
		opts.TimeoutMS < 10 || opts.TimeoutMS > 300000 ||
		opts.SuiteTimeoutMS < opts.TimeoutMS || opts.SuiteTimeoutMS > 600000 ||
		!slices.Contains([]string{"approve", "edit", "reject"}, opts.ApprovalPolicy) ||
		!slices.Contains([]string{"live_model", "deterministic_http_fixture"}, opts.EvidenceMode) {
		return report, errors.New("require safe target, credential, 1-20 trials, positive shared and per-Run budgets, bounded deadlines and explicit approval/evidence policy")
	}
	data, hash, err := loadDataset(opts.DatasetPath)
	if err != nil {
		return report, err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(opts.SuiteTimeoutMS)*time.Millisecond)
	defer cancel()
	h, err := newHost(ctx, data, opts)
	if err != nil {
		return report, err
	}
	defer func() {
		if cleanupErr := h.close(); cleanupErr != nil {
			report.Gate.Passed = false
			report.Gate.Reasons = append(report.Gate.Reasons, "evaluation_cleanup_failed")
			err = errors.Join(err, cleanupErr)
		}
	}()
	report = Report{
		Identity: evalreport.Identity{
			ReportFormat: evalreport.Format, EvaluationKind: "orchestration_ablation",
			DatasetID: data.ID, DatasetVersion: data.Version, DatasetHash: hash,
			GitRevision: opts.Revision, StartedAt: time.Now().UTC(),
		},
		SchemaVersion: "orchestration-mode-eval-v1", ExperimentAxis: "orchestration_mode",
		SensorVersion: "sourced-json-and-durable-read-v1", Options: opts, Dataset: data, Samples: []Sample{},
		Limitations: []string{
			"Mode-specific prompts, Stage sequencing and approval are the tested orchestration bundle; final model inputs are not identical.",
			"Empty Memory/Knowledge corpus with local fixed embedding transport; task evidence comes only from the scoped source file.",
			"Tokens use production estimated preflight reservations and authoritative usage when available; no guarantee against provider-side billing overshoot.",
			"Suite token admission reserves the same full per-Run ceiling before each arm, then returns unused capacity after settlement.",
			"No live reliability claim from deterministic fixtures; p95 is intentionally null for this small profile.",
			"Run identities and Replay are retained in the report; isolated database and temporary files are removed after execution.",
		},
	}
	stop := ""
	for trial := 1; trial <= opts.Trials; trial++ {
		modes := []string{"single", "multi_agent", "autonomous"}
		offset := (trial - 1) % 3
		modes = append(modes[offset:], modes[:offset]...)
		for _, mode := range modes {
			s := Sample{Trial: trial, Mode: mode, Status: "not_evaluated", Findings: []string{}, Approvals: []Approval{}, StageMS: map[string]int64{}}
			if stop == "" && ctx.Err() != nil {
				stop = "canceled"
			}
			if stop == "" && (h.guard.count() >= opts.MaxRequests || opts.MaxTokens-report.TotalTokens < opts.SampleTokens) {
				stop = "suite_budget_exhausted"
			}
			if stop != "" {
				s.StopReason = stop
			} else {
				if err = h.sample(ctx, data, opts, &s); err != nil {
					s.Status = "failed"
					s.StopReason = "evaluation_infrastructure_error"
					s.Findings = append(s.Findings, "evaluation_infrastructure_error")
					stop = s.StopReason
				}
				report.TotalTokens += s.Usage.TotalTokens
				if s.Usage.OpenReservations > 0 {
					stop = "unsettled_model_usage"
				}
			}
			report.Samples = append(report.Samples, s)
		}
	}
	report.PhysicalRequests = h.guard.count()
	summarize(&report)
	report.CompletedAt = time.Now().UTC()
	return report, err
}
