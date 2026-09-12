package agent

import (
	"fmt"
	"strings"

	"agentflow-platform/apps/api/internal/domain"
)

const (
	// V1 and V2 remain executable only for offline migration baselines.
	AgentSelectionPolicyVersionV1 = "agent-selection-v1"
	AgentSelectionPolicyVersionV2 = "agent-selection-v2"

	CurrentAgentSelectionPolicyVersion = "agent-selection-v3"
)

type agentSelectionPolicy struct {
	Revision                   string
	ThresholdSource            string
	MinimumScore               int
	MinimumScoreMargin         int
	MinimumLLMConfidence       float64
	MinimumRequirementCoverage float64
}

func selectionPolicy(revision string) (agentSelectionPolicy, bool) {
	switch revision {
	case AgentSelectionPolicyVersionV1, AgentSelectionPolicyVersionV2:
		return agentSelectionPolicy{Revision: revision}, true
	case CurrentAgentSelectionPolicyVersion:
		// Dataset v1 recommends 4/1, but the production policy intentionally
		// retains these conservative defaults until live evidence is reviewed.
		return agentSelectionPolicy{
			Revision:                   revision,
			ThresholdSource:            "conservative-safety-baseline-v1",
			MinimumScore:               capabilityHintWeight,
			MinimumScoreMargin:         1,
			MinimumLLMConfidence:       0.5,
			MinimumRequirementCoverage: 1,
		}, true
	default:
		return agentSelectionPolicy{}, false
	}
}

type routeGateEvidence struct {
	ProposedAgentID      string
	ThresholdSource      string
	MinimumScore         int
	MinimumScoreMargin   int
	MinimumLLMConfidence float64
	MinimumCoverage      float64
	TopScore             int
	RunnerUpScore        int
	ScoreMargin          int
	Confidence           float64
	RequirementCoverage  float64
	ReasonCodes          []string
}

func applySelectionGate(decision *routeDecision, policyRevision string, eligibility []agentEligibility) error {
	policy, ok := selectionPolicy(policyRevision)
	if !ok {
		return fmt.Errorf("unsupported agent selection policy %q", policyRevision)
	}
	if policy.ThresholdSource == "" {
		return nil
	}
	evidence := routeGateEvidence{
		ProposedAgentID: decision.Agent.ID, ThresholdSource: policy.ThresholdSource, MinimumScore: policy.MinimumScore,
		MinimumScoreMargin: policy.MinimumScoreMargin, MinimumLLMConfidence: policy.MinimumLLMConfidence,
		MinimumCoverage: policy.MinimumRequirementCoverage, TopScore: decision.Score,
		Confidence: decision.Confidence, RequirementCoverage: selectedRequirementCoverage(decision.Agent.ID, eligibility),
	}
	if len(decision.Scores) > 1 {
		evidence.RunnerUpScore = decision.Scores[1].Score
		evidence.ScoreMargin = decision.Score - evidence.RunnerUpScore
	} else {
		evidence.ScoreMargin = decision.Score
	}
	if decision.Score < policy.MinimumScore {
		evidence.ReasonCodes = append(evidence.ReasonCodes, "minimum_score_not_met")
	}
	if len(decision.Scores) > 1 && evidence.ScoreMargin < policy.MinimumScoreMargin {
		evidence.ReasonCodes = append(evidence.ReasonCodes, "minimum_score_margin_not_met")
	}
	if decision.Mode == "llm" && decision.Confidence < policy.MinimumLLMConfidence {
		evidence.ReasonCodes = append(evidence.ReasonCodes, "minimum_llm_confidence_not_met")
	}
	if evidence.RequirementCoverage < policy.MinimumRequirementCoverage {
		evidence.ReasonCodes = append(evidence.ReasonCodes, "minimum_requirement_coverage_not_met")
	}
	decision.Gate = evidence
	if len(evidence.ReasonCodes) == 0 {
		return nil
	}
	decision.Reason = "Selection did not pass policy gates: " + strings.Join(evidence.ReasonCodes, ", ") + "."
	decision.Agent = domain.Agent{}
	return ErrNoSuitableAgent
}

func selectedRequirementCoverage(agentID string, eligibility []agentEligibility) float64 {
	for _, candidate := range eligibility {
		if candidate.Agent.ID == agentID {
			return candidate.RequirementCoverage
		}
	}
	return 0
}
