package agent

import (
	"errors"
	"fmt"
	"strings"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/failure"
	"agentflow-platform/apps/api/internal/tools"
)

// RoutingEvaluationInput is the narrow offline-evaluation entry point for the
// production Agent selection policy. ModelResponse and ModelError are mutually
// exclusive and are only used when RouterMode is auto.
type RoutingEvaluationInput struct {
	Agents         []domain.Agent
	Catalog        *tools.Catalog
	Task           string
	Plan           string
	Requirements   domain.AgentRoutingRequirements
	PolicyRevision string
	RouterMode     string
	ModelResponse  string
	ModelError     error
}

const AgentRoutingPromptRevision = "agent-router-json-v1"

type RoutingEvaluationPolicy struct {
	Revision                   string  `json:"revision"`
	ThresholdSource            string  `json:"threshold_source,omitempty"`
	MinimumScore               int     `json:"minimum_score"`
	MinimumScoreMargin         int     `json:"minimum_score_margin"`
	MinimumLLMConfidence       float64 `json:"minimum_llm_confidence"`
	MinimumRequirementCoverage float64 `json:"minimum_requirement_coverage"`
}

func AgentSelectionPolicyEvidence(revision string) (RoutingEvaluationPolicy, bool) {
	policy, ok := selectionPolicy(revision)
	if !ok {
		return RoutingEvaluationPolicy{}, false
	}
	return RoutingEvaluationPolicy{
		Revision: policy.Revision, ThresholdSource: policy.ThresholdSource,
		MinimumScore: policy.MinimumScore, MinimumScoreMargin: policy.MinimumScoreMargin,
		MinimumLLMConfidence: policy.MinimumLLMConfidence, MinimumRequirementCoverage: policy.MinimumRequirementCoverage,
	}, true
}

type RoutingEvaluationCandidate struct {
	AgentID             string   `json:"agent_id"`
	Eligible            bool     `json:"eligible"`
	Score               int      `json:"score"`
	ExclusionReasons    []string `json:"exclusion_reasons,omitempty"`
	RequirementCoverage float64  `json:"requirement_coverage"`
}

type RoutingEvaluationResult struct {
	Outcome             string                       `json:"outcome"`
	SelectedAgentID     string                       `json:"selected_agent_id,omitempty"`
	ProposedAgentID     string                       `json:"proposed_agent_id,omitempty"`
	PolicyRevision      string                       `json:"policy_revision"`
	Mode                string                       `json:"mode"`
	Reason              string                       `json:"reason"`
	FailureCode         string                       `json:"failure_code,omitempty"`
	FallbackReasonCode  string                       `json:"fallback_reason_code,omitempty"`
	InvalidResponse     bool                         `json:"invalid_response"`
	TopScore            int                          `json:"top_score"`
	RunnerUpScore       int                          `json:"runner_up_score"`
	ScoreMargin         int                          `json:"score_margin"`
	Confidence          float64                      `json:"confidence"`
	RequirementCoverage float64                      `json:"requirement_coverage"`
	ThresholdSource     string                       `json:"threshold_source,omitempty"`
	Candidates          []RoutingEvaluationCandidate `json:"candidates"`
}

// AgentRoutingPrompts returns the exact prompts used by the production LLM
// Router after hard eligibility filtering.
func AgentRoutingPrompts(task, plan string, requirements domain.AgentRoutingRequirements, agents []domain.Agent) (string, string) {
	return routerSystemPrompt(), routerUserPrompt(task, plan, requirements, agents)
}

// EvaluateAgentRouting runs the same policy primitives as production without
// creating Runs, stages, events, or child Runs.
func EvaluateAgentRouting(input RoutingEvaluationInput) RoutingEvaluationResult {
	input.PolicyRevision = strings.TrimSpace(input.PolicyRevision)
	if input.PolicyRevision == "" {
		input.PolicyRevision = CurrentAgentSelectionPolicyVersion
	}
	input.Requirements = domain.NormalizeAgentRoutingRequirements(input.Requirements)
	result := RoutingEvaluationResult{PolicyRevision: input.PolicyRevision, Mode: NormalizeRouterMode(input.RouterMode), Candidates: []RoutingEvaluationCandidate{}}
	if !input.Requirements.IsEmpty() && input.PolicyRevision != CurrentAgentSelectionPolicyVersion {
		return failedEvaluation(result, AgentSelectionOutcomeRouterFailed, ErrInvalidRoutingRequirements,
			fmt.Sprintf("frozen policy %q does not support routing requirements", input.PolicyRevision))
	}
	if conflicts := input.Requirements.ConflictingTools(); len(conflicts) > 0 {
		return failedEvaluation(result, AgentSelectionOutcomeRouterFailed, ErrInvalidRoutingRequirements,
			"conflicting tools: "+strings.Join(conflicts, ", "))
	}

	eligible, eligibility := eligibleWorkerAgents(input.Agents, input.Catalog, input.Requirements)
	if len(eligible) == 0 {
		result.Outcome = AgentSelectionOutcomeNoEligible
		result.Reason = "Every frozen candidate failed a hard capability check."
		result.FailureCode = failure.Describe(ErrNoEligibleAgent).Code
		return evaluationResult(result, routeDecision{}, eligibility)
	}

	var decision routeDecision
	var err error
	if result.Mode == RouterModeQuery {
		decision, err = selectDeterministicPolicyForEvaluation(input.PolicyRevision, eligible, input.Task, input.Plan, input.Requirements)
	} else if input.ModelError != nil {
		err = input.ModelError
	} else {
		decision, err = parseLLMRouteDecisionWithPolicy(input.ModelResponse, eligible, true)
		if err != nil {
			err = fmt.Errorf("%w: %v", ErrAgentRouteResponseInvalid, err)
			result.InvalidResponse = true
		}
	}
	if err != nil && result.Mode == RouterModeAuto && shouldFallbackAgentSelection(err) {
		decision, err = selectDeterministicPolicyForEvaluation(input.PolicyRevision, eligible, input.Task, input.Plan, input.Requirements)
		decision.FallbackReasonCode = failure.Describe(input.ModelError).Code
		if result.InvalidResponse {
			decision.FallbackReasonCode = failure.Describe(ErrAgentRouteResponseInvalid).Code
		}
	}
	if err != nil {
		result.Outcome = AgentSelectionOutcomeRouterFailed
		if errors.Is(err, ErrNoSuitableAgent) {
			result.Outcome = AgentSelectionOutcomeNoSuitable
		}
		result.Reason = err.Error()
		result.FailureCode = failure.Describe(err).Code
		return evaluationResult(result, decision, eligibility)
	}
	if err = applySelectionGate(&decision, input.PolicyRevision, eligibility); err != nil {
		result.Outcome = AgentSelectionOutcomeNoSuitable
		result.FailureCode = failure.Describe(err).Code
	} else {
		result.Outcome = AgentSelectionOutcomeSelected
	}
	result.InvalidResponse = result.InvalidResponse || errors.Is(input.ModelError, ErrAgentRouteResponseInvalid)
	return evaluationResult(result, decision, eligibility)
}

func selectDeterministicPolicyForEvaluation(policyVersion string, agents []domain.Agent, task string, plan string, requirements domain.AgentRoutingRequirements) (routeDecision, error) {
	switch policyVersion {
	case AgentSelectionPolicyVersionV1:
		return selectWorkerAgentV1(agents, task, plan), nil
	case AgentSelectionPolicyVersionV2:
		return selectWorkerAgentV2Baseline(agents, task, plan, requirements)
	case CurrentAgentSelectionPolicyVersion:
		return rankWorkerAgentsDeclarative(agents, task, plan, requirements), nil
	default:
		return routeDecision{}, fmt.Errorf("unsupported agent selection policy %q", policyVersion)
	}
}

func failedEvaluation(result RoutingEvaluationResult, outcome string, err error, reason string) RoutingEvaluationResult {
	result.Outcome, result.Reason, result.FailureCode = outcome, reason, failure.Describe(err).Code
	return result
}

func evaluationResult(result RoutingEvaluationResult, decision routeDecision, eligibility []agentEligibility) RoutingEvaluationResult {
	if decision.Mode != "" {
		result.Mode = decision.Mode
	}
	result.SelectedAgentID = decision.Agent.ID
	result.ProposedAgentID = decision.Gate.ProposedAgentID
	if result.ProposedAgentID == "" {
		result.ProposedAgentID = decision.Agent.ID
	}
	result.Reason = firstNonEmpty(decision.Reason, result.Reason)
	result.FallbackReasonCode = decision.FallbackReasonCode
	result.TopScore = decision.Gate.TopScore
	result.RunnerUpScore = decision.Gate.RunnerUpScore
	result.ScoreMargin = decision.Gate.ScoreMargin
	result.Confidence = decision.Confidence
	result.RequirementCoverage = decision.Gate.RequirementCoverage
	result.ThresholdSource = decision.Gate.ThresholdSource
	if decision.Gate.ThresholdSource == "" {
		result.TopScore = decision.Score
		if len(decision.Scores) > 1 {
			result.RunnerUpScore = decision.Scores[1].Score
			result.ScoreMargin = decision.Score - result.RunnerUpScore
		} else {
			result.ScoreMargin = decision.Score
		}
		result.RequirementCoverage = selectedRequirementCoverage(result.ProposedAgentID, eligibility)
	}
	scores := make(map[string]int, len(decision.Scores))
	for _, score := range decision.Scores {
		scores[score.Agent.ID] = score.Score
	}
	for _, candidate := range eligibility {
		result.Candidates = append(result.Candidates, RoutingEvaluationCandidate{
			AgentID: candidate.Agent.ID, Eligible: candidate.Eligible, Score: scores[candidate.Agent.ID],
			ExclusionReasons: append([]string(nil), candidate.ExclusionReasons...), RequirementCoverage: candidate.RequirementCoverage,
		})
	}
	return result
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
