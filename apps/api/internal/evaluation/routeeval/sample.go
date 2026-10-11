package routeeval

import (
	"agentflow-platform/apps/api/internal/agent"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/inference/provider"
	"agentflow-platform/apps/api/internal/redaction"
	"agentflow-platform/apps/api/internal/tool"
	"context"
	"time"
)

func evaluateSample(ctx context.Context, completer provider.TextCompleter, catalog *tool.Catalog, agents []domain.Agent, item EvaluationCase, opts Options, remainingTokens int) (agent.RoutingEvaluationResult, provider.Usage, int64, string) {
	input := agent.RoutingEvaluationInput{Agents: agents, Catalog: catalog, Task: item.Task, Plan: item.Plan,
		Requirements: item.Requirements, RouterMode: opts.RouterMode}
	started := time.Now()
	if opts.RouterMode == agent.RouterModeAuto {
		eligible := eligibleAgents(agents, catalog, item.Requirements)
		if len(eligible) == 0 {
			return agent.EvaluateAgentRouting(input), provider.Usage{}, time.Since(started).Milliseconds(), ""
		}
		systemPrompt, userPrompt := agent.AgentRoutingPrompts(item.Task, item.Plan, item.Requirements, eligible)
		estimatedPromptTokens := estimateTokens(systemPrompt + "\n" + userPrompt)
		if remainingTokens <= estimatedPromptTokens {
			return agent.RoutingEvaluationResult{Outcome: agent.AgentSelectionOutcomeRouterFailed, FailureCode: "suite_token_budget_exhausted", Reason: "suite token budget exhausted before model request"}, provider.Usage{}, time.Since(started).Milliseconds(), ""
		}
		callCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
		completion, err := completer.CompleteTextDetailed(callCtx, systemPrompt, userPrompt)
		cancel()
		input.ModelResponse, input.ModelError = completion.Text, err
		usage := completion.Usage
		if !usage.Valid() {
			usage = provider.Usage{PromptTokens: estimatedPromptTokens, CompletionTokens: estimateTokens(completion.Text), Estimated: true}
			usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
		}
		return agent.EvaluateAgentRouting(input), usage, time.Since(started).Milliseconds(), completion.Model
	}
	return agent.EvaluateAgentRouting(input), provider.Usage{}, time.Since(started).Milliseconds(), ""
}

func applyResult(sample *Sample, result agent.RoutingEvaluationResult, usage provider.Usage, latency int64, actualModel string) {
	sample.ActualOutcome, sample.DecisionMode, sample.SelectedAgentID, sample.ProposedAgentID = result.Outcome, result.Mode, result.SelectedAgentID, result.ProposedAgentID
	sample.InvalidResponse, sample.FailureCode, sample.Reason = result.InvalidResponse, result.FailureCode, result.Reason
	sample.TopScore, sample.RunnerUpScore, sample.ScoreMargin = result.TopScore, result.RunnerUpScore, result.ScoreMargin
	sample.Confidence, sample.RequirementCoverage, sample.Candidates = result.Confidence, result.RequirementCoverage, result.Candidates
	sample.FallbackAttempted = result.FallbackReasonCode != ""
	sample.FallbackRecovered = sample.FallbackAttempted && result.Outcome != agent.AgentSelectionOutcomeRouterFailed
	for _, candidate := range result.Candidates {
		if candidate.Eligible {
			sample.EligibleAgentIDs = append(sample.EligibleAgentIDs, candidate.AgentID)
		}
	}
	sample.EligibleRecall = intersects(sample.EligibleAgentIDs, sample.AcceptableAgentIDs)
	sample.AcceptableSelection = contains(sample.AcceptableAgentIDs, sample.SelectedAgentID)
	sample.UnsafeFalseRoute = result.Outcome == agent.AgentSelectionOutcomeSelected && !sample.AcceptableSelection
	sample.ModelCalls, sample.InputTokens, sample.OutputTokens = 0, usage.PromptTokens, usage.CompletionTokens
	sample.RouterTokens, sample.UsageEstimated, sample.LatencyMS = usage.TotalTokens, usage.Estimated, latency
	sample.ActualModel = actualModel
	if usage.Valid() {
		sample.ModelCalls = 1
	}
	if result.Outcome == agent.AgentSelectionOutcomeRouterFailed {
		sample.Status = "failed"
	}
	if result.FailureCode == "suite_token_budget_exhausted" {
		sample.Status = "not_evaluated"
	}
	if sample.Reason != "" {
		sample.Reason, _ = redaction.Text(sample.Reason)
	}
}

func eligibleAgents(agents []domain.Agent, catalog *tool.Catalog, requirements domain.AgentRoutingRequirements) []domain.Agent {
	result := make([]domain.Agent, 0, len(agents))
	for _, item := range agent.EvaluateAgentRouting(agent.RoutingEvaluationInput{Agents: agents, Catalog: catalog, Task: "", Plan: "",
		Requirements: requirements, RouterMode: agent.RouterModeQuery}).Candidates {
		if item.Eligible {
			for _, candidate := range agents {
				if candidate.ID == item.AgentID {
					result = append(result, candidate)
					break
				}
			}
		}
	}
	return result
}

func estimateTokens(value string) int {
	if value == "" {
		return 0
	}
	return max(1, (len(value)+3)/4)
}
