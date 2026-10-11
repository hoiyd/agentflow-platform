// Package routeeval benchmarks the production Agent selection policy against a
// frozen calibration/holdout dataset. It never creates runtime Runs or stages.
package routeeval

import (
	"agentflow-platform/apps/api/internal/agent"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/evaluation/evalreport"
	"agentflow-platform/apps/api/internal/inference/provider"
	"agentflow-platform/apps/api/internal/tool"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	SchemaVersion        = "agent-routing-eval-v1"
	DatasetSchemaVersion = "agent-routing-dataset-v1"
	PromptRevision       = agent.AgentRoutingPromptRevision
	SplitCalibration     = "calibration"
	SplitHoldout         = "holdout"
)

type Options struct {
	DatasetPath    string        `json:"dataset_path"`
	RouterMode     string        `json:"router_mode"`
	Trials         int           `json:"trials"`
	MaxModelCalls  int           `json:"max_model_calls,omitempty"`
	MaxTotalTokens int           `json:"max_total_tokens,omitempty"`
	Timeout        time.Duration `json:"sample_timeout_ns,omitempty"`
	Revision       string        `json:"git_revision"`
	Model          string        `json:"model,omitempty"`
	Provider       string        `json:"provider,omitempty"`
}

func Run(ctx context.Context, completer provider.TextCompleter, opts Options) (Report, error) {
	data, datasetHash, err := loadDataset(opts.DatasetPath)
	if err != nil {
		return Report{}, err
	}
	policy := agent.AgentSelectionPolicyEvidence()
	if opts.RouterMode == "" {
		opts.RouterMode = agent.RouterModeQuery
	}
	if opts.RouterMode != agent.RouterModeQuery && opts.RouterMode != agent.RouterModeAuto {
		return Report{}, fmt.Errorf("router mode must be %q or %q", agent.RouterModeQuery, agent.RouterModeAuto)
	}
	if opts.RouterMode == agent.RouterModeQuery {
		opts.Trials = 1
	} else if completer == nil || opts.Trials < 1 || opts.Trials > 20 || opts.MaxModelCalls < 1 || opts.MaxTotalTokens < 1 || opts.Timeout <= 0 || opts.Timeout > 5*time.Minute {
		return Report{}, errors.New("live routing requires a model client, 1-20 trials, positive call/token budgets and sample timeout <= 5m")
	}
	catalog := tool.DefaultCatalog()
	systemPrompt, _ := agent.AgentRoutingPrompts("", "", domain.AgentRoutingRequirements{}, nil)
	startedAt := time.Now().UTC()
	report := Report{
		Identity: evalreport.Identity{ReportFormat: evalreport.Format, EvaluationKind: "agent_routing", DatasetID: data.ID,
			DatasetVersion: data.Version, DatasetHash: datasetHash, GitRevision: normalizeRevision(opts.Revision), StartedAt: startedAt},
		SchemaVersion: SchemaVersion,
		Config: Config{Policy: policy, RouterMode: opts.RouterMode, PromptRevision: PromptRevision,
			PromptHash: digest([]byte(systemPrompt)), AgentCatalogHash: agentCatalogHash(data.Agents), Model: opts.Model,
			Provider: opts.Provider, Trials: opts.Trials, MaxModelCalls: opts.MaxModelCalls,
			MaxTotalTokens: opts.MaxTotalTokens, SampleTimeoutMS: opts.Timeout.Milliseconds()},
		CostSource: "not_applicable: deterministic routing with no model request",
		Samples:    []Sample{}, Summary: map[string]Summary{},
	}
	if opts.RouterMode == agent.RouterModeAuto {
		report.CostSource = "unavailable: no price table; router token usage only"
	}
	remainingCalls, remainingTokens := opts.MaxModelCalls, opts.MaxTotalTokens
	stop := ""
	for trial := 1; trial <= opts.Trials; trial++ {
		for _, item := range data.Cases {
			sample := Sample{CaseID: item.ID, Split: item.Split, Trial: trial, Coverage: append([]string(nil), item.Coverage...),
				Status: "completed", ExpectedOutcome: item.ExpectedOutcome, AcceptableAgentIDs: append([]string(nil), item.AcceptableAgentIDs...),
				EligibleAgentIDs: []string{}, Candidates: []agent.RoutingEvaluationCandidate{}}
			if ctx.Err() != nil {
				stop = "canceled"
			}
			if opts.RouterMode == agent.RouterModeAuto && (remainingCalls <= 0 || remainingTokens <= 0) {
				stop = "suite_budget_exhausted"
			}
			if stop != "" {
				sample.Status, sample.FailureCode, sample.Reason = "not_evaluated", stop, stop
			} else {
				result, usage, latency, actualModel := evaluateSample(ctx, completer, catalog, data.Agents, item, opts, remainingTokens)
				applyResult(&sample, result, usage, latency, actualModel)
				remainingCalls -= sample.ModelCalls
				remainingTokens -= sample.RouterTokens
			}
			report.Samples = append(report.Samples, sample)
		}
	}
	report.Summary = summarizeBySplit(report.Samples)
	report.Calibration = calibrate(report.Samples)
	report.Gate = gate(report.Samples)
	report.CompletedAt = time.Now().UTC()
	return report, nil
}

func normalizeRevision(value string) string {
	if strings.TrimSpace(value) == "" {
		return "unknown"
	}
	return strings.TrimSpace(value)
}
