package contexteval

import (
	"context"
	"errors"
	"strconv"
	"time"

	"agentflow-platform/apps/api/internal/contextassembly"
	"agentflow-platform/apps/api/internal/contextcompaction"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/evaluation/evalreport"
	"agentflow-platform/apps/api/internal/inference/openai"
)

const liveReportSchema = "live-compaction-continuation-v1"

// RunLive uses production Compactor/Assembly/Turn/Tools, not production HTTP or
// mode orchestration. The report retains isolated fixture evidence, not durable
// production Runs that can be opened or resumed through the application API.
// A dedicated client is required: physical retries are disabled for suite bounds.
func RunLive(ctx context.Context, client *openai.Client, opts LiveOptions) (LiveReport, error) {
	if client == nil || !client.HasAPIKey() || opts.Trials < 1 || opts.Trials > 20 || opts.MaxModelCalls < 1 || opts.MaxTotalTokens < 1 || opts.Timeout <= 0 || opts.Timeout > 5*time.Minute ||
		(opts.EvidenceMode != "live_model" && opts.EvidenceMode != "deterministic_http_fixture") || opts.ContextWindowTokens < 0 || opts.SummaryMaxTokens < 0 || opts.OutputReserveTokens < 0 {
		return LiveReport{}, errors.New("require dedicated credentialed client, 1-20 trials, positive shared call/token budgets, timeout <=5m and explicit evidence mode")
	}
	data, hash, err := loadLiveDataset(opts.DatasetPath)
	if err != nil {
		return LiveReport{}, err
	}
	config := contextassembly.DefaultConfig()
	config.ContextWindowTokens, config.HistoryMaxTokens = 32768, 30000
	config.OutputReserveTokens, config.SafetyMarginTokens = 2048, 512
	config.CompactionRecentTokens, config.CompactionSummaryMaxTokens = 128, 1024
	config.HistoryRetrievalEnabled = false
	if opts.ContextWindowTokens > 0 {
		config.ContextWindowTokens = opts.ContextWindowTokens
	}
	if opts.SummaryMaxTokens > 0 {
		config.CompactionSummaryMaxTokens = opts.SummaryMaxTokens
	}
	if opts.OutputReserveTokens > 0 {
		// Generation includes reasoning as well as answer text; it is not the stored summary cap.
		config.OutputReserveTokens = opts.OutputReserveTokens
		if config.OutputReserveTokens >= config.ContextWindowTokens-config.SafetyMarginTokens {
			return LiveReport{}, errors.New("output reserve plus safety margin must leave a positive input budget")
		}
	}
	config = contextassembly.NormalizeConfig(config)
	identity := client.RuntimeIdentity()
	client.SetRetryPolicy(openai.RetryPolicy{MaxAttempts: 1})
	requests := &liveRequestBudget{max: opts.MaxModelCalls}
	client.SetRequestLimiter(requests)
	report := LiveReport{Identity: evalreport.Identity{ReportFormat: evalreport.Format, EvaluationKind: "compaction_fidelity_continuation",
		DatasetID: data.ID, DatasetVersion: data.Version, DatasetHash: hash, GitRevision: normalizeRevision(opts.Revision), StartedAt: time.Now().UTC()},
		SchemaVersion: liveReportSchema, Config: opts, Model: identity.Model, Provider: identity.Provider, Sampling: identity.GenerationPolicy,
		Assembly: config, Algorithm: contextcompaction.AlgorithmVersion, SensorVersion: "typed-claims-exact-provenance-v3",
		PromptHash: liveDigest(probeSystem + continuationSystem + responseInstructions), Dataset: data,
		CostSource: "unknown: no evaluation price table; ledger costs are not a claim of zero monetary cost", Samples: []LiveSample{}}
	remainingCalls, remainingTokens, stop := opts.MaxModelCalls, opts.MaxTotalTokens, ""
	for trial := 1; trial <= opts.Trials; trial++ {
		for _, c := range data.Cases {
			arms := []string{StrategyFullHistory, StrategyCompacted}
			if trial%2 == 0 {
				arms[0], arms[1] = arms[1], arms[0]
			}
			for _, arm := range arms {
				s := LiveSample{CaseID: c.ID, Trial: trial, Arm: arm, Status: "not_evaluated", Fidelity: LiveCheck{Findings: []string{}}, Outcome: LiveCheck{Findings: []string{}}}
				if ctx.Err() != nil {
					stop = "canceled"
				}
				if remainingCalls <= 0 || remainingTokens <= 0 {
					stop = "suite_budget_exhausted"
				}
				if stop != "" {
					s.Reason = stop
				} else {
					before := requests.count()
					limits := domain.RuntimeRunBudget{MaxModelCalls: remainingCalls, MaxTotalTokens: remainingTokens, MaxToolCalls: 4, MaxRuntimeMS: opts.Timeout.Milliseconds()}
					contractHash, err := runLiveSample(ctx, client, config, limits, opts.Timeout, c, &s)
					if err != nil {
						return report, err
					}
					report.ToolContractHash = contractHash
					s.PhysicalRequests = requests.count() - before
					remainingCalls -= s.PhysicalRequests
					remainingTokens -= s.Ledger.Totals.TotalTokens
					if s.Ledger.Totals.OpenReservations > 0 {
						stop = "unsettled_model_usage"
					}
				}
				report.Samples = append(report.Samples, s)
			}
		}
	}
	report.Summary, report.Gate, report.Pairs = summarizeLive(report.Samples)
	report.PhysicalRequests = requests.count()
	for _, s := range report.Samples {
		u := s.Ledger.Totals
		report.Usage.ModelCalls += u.ModelCalls
		report.Usage.ToolCalls += u.ToolCalls
		report.Usage.PromptTokens += u.PromptTokens
		report.Usage.CompletionTokens += u.CompletionTokens
		report.Usage.TotalTokens += u.TotalTokens
		report.Usage.OpenReservations += u.OpenReservations
		report.Usage.CostUnknownEntries += u.CostUnknownEntries
	}
	report.CompletedAt = time.Now().UTC()
	return report, nil
}

func summarizeLive(samples []LiveSample) (LiveSummary, evalreport.Gate, []LivePair) {
	summary := LiveSummary{Samples: len(samples)}
	gate := evalreport.Gate{Passed: len(samples) > 0, Reasons: []string{}}
	byPair := map[string]map[string]LiveSample{}
	order := []string{}
	for _, s := range samples {
		if s.Status == "not_evaluated" || s.Status == "infeasible" {
			summary.NotEvaluated++
		}
		verified := liveVerified(s)
		if verified {
			summary.Verified++
		}
		if s.Arm == StrategyCompacted {
			gate.BlockingSamples++
			if !verified {
				gate.Passed = false
				gate.BlockingFailures++
				gate.Reasons = append(gate.Reasons, s.CaseID+": candidate fidelity/continuation not verified")
			}
		}
		key := s.CaseID + "/" + strconv.Itoa(s.Trial)
		if byPair[key] == nil {
			byPair[key] = map[string]LiveSample{}
			order = append(order, key)
		}
		byPair[key][s.Arm] = s
	}
	pairs := []LivePair{}
	for _, key := range order {
		p := byPair[key]
		summary.Pairs++
		a, aok := p[StrategyFullHistory]
		b, bok := p[StrategyCompacted]
		pair := LivePair{CaseID: a.CaseID, Trial: a.Trial, BaselineRunID: a.RunID, CandidateRunID: b.RunID}
		if !aok || !bok || a.Status != "completed" || b.Status != "completed" || b.Fallback || a.BoundaryHash != b.BoundaryHash || a.StateHash != b.StateHash || a.TaskState.Version != b.TaskState.Version {
			pair.Reason = "incomplete, infeasible, fallback or mismatched source/state boundary"
			pairs = append(pairs, pair)
			continue
		}
		pair.FidelityRegressed = a.Fidelity.Passed && !b.Fidelity.Passed
		pair.OutcomeRegressed = a.Outcome.Passed && !b.Outcome.Passed
		totalTokens := b.Ledger.Totals.TotalTokens - a.Ledger.Totals.TotalTokens
		continuationTokens := 0
		var continuationMS int64
		for _, phase := range b.Phases {
			if phase.Name == "compaction" {
				pair.CompactionCalls = phase.Usage.ModelCalls
				pair.CompactionTokens = phase.Usage.TotalTokens
				pair.CompactionMS = phase.DurationMS
			}
			if phase.Name == "continuation" {
				continuationTokens += phase.Usage.TotalTokens
				continuationMS += phase.DurationMS
			}
		}
		for _, phase := range a.Phases {
			if phase.Name == "continuation" {
				continuationTokens -= phase.Usage.TotalTokens
				continuationMS -= phase.DurationMS
			}
		}
		pair.TotalTokenDelta = &totalTokens
		pair.ContinuationTokenDelta = &continuationTokens
		pair.ContinuationMSDelta = &continuationMS
		pairs = append(pairs, pair)
		summary.Comparable++
		if liveVerified(a) && !liveVerified(b) {
			summary.Regressed++
		} else if !liveVerified(a) && liveVerified(b) {
			summary.Improved++
		} else {
			summary.Unchanged++
		}
	}
	if summary.Comparable != summary.Pairs {
		gate.Passed = false
		gate.Reasons = append(gate.Reasons, "incomplete/infeasible/fallback pairs cannot establish compaction benefit")
	}
	return summary, gate, pairs
}

func liveVerified(s LiveSample) bool {
	return s.Status == "completed" && !s.Fallback && s.HistoryUnchanged && s.Fidelity.Passed && s.Outcome.Passed
}
