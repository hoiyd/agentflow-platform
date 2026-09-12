package agent

import (
	"errors"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/tools"
)

func TestRoutingEvaluationUsesProductionGateAndFallback(t *testing.T) {
	agents := store.DefaultAgents(time.Now().UTC())
	policy := AgentSelectionPolicyEvidence()
	if policy.MinimumScore != 6 || policy.ThresholdSource == "" {
		t.Fatalf("missing policy evidence: %+v", policy)
	}
	systemPrompt, userPrompt := AgentRoutingPrompts("task", "plan", domain.AgentRoutingRequirements{}, agents)
	if systemPrompt == "" || userPrompt == "" {
		t.Fatal("routing prompts are empty")
	}
	selected := EvaluateAgentRouting(RoutingEvaluationInput{Agents: agents, Catalog: tools.DefaultCatalog(), Task: "Implement and test a Go API",
		RouterMode: RouterModeQuery})
	if selected.Outcome != AgentSelectionOutcomeSelected || selected.SelectedAgentID != "agent_coding" || selected.TopScore == 0 || selected.ProposedAgentID != "agent_coding" {
		t.Fatalf("unexpected deterministic result: %+v", selected)
	}

	fallback := EvaluateAgentRouting(RoutingEvaluationInput{Agents: agents, Catalog: tools.DefaultCatalog(), Task: "Research market pricing sources",
		RouterMode: RouterModeAuto, ModelResponse: "bad json"})
	if fallback.Outcome != AgentSelectionOutcomeSelected || fallback.SelectedAgentID != "agent_research" || !fallback.InvalidResponse || fallback.FallbackReasonCode == "" {
		t.Fatalf("unexpected fallback result: %+v", fallback)
	}

	failed := EvaluateAgentRouting(RoutingEvaluationInput{Agents: agents, Catalog: tools.DefaultCatalog(), Task: "Research market pricing sources",
		RouterMode: RouterModeAuto, ModelError: errors.New("auth failed")})
	if failed.Outcome != AgentSelectionOutcomeRouterFailed || failed.FailureCode == "" {
		t.Fatalf("unexpected router failure: %+v", failed)
	}
}

func TestRoutingEvaluationRejectsInvalidRequirements(t *testing.T) {
	result := EvaluateAgentRouting(RoutingEvaluationInput{Agents: store.DefaultAgents(time.Now().UTC()), Catalog: tools.DefaultCatalog(),
		RouterMode:   RouterModeQuery,
		Requirements: domain.AgentRoutingRequirements{RequiredTools: []string{"calculator"}, ProhibitedTools: []string{"calculator"}}})
	if result.Outcome != AgentSelectionOutcomeRouterFailed || result.FailureCode != "agent_route_requirements_invalid" {
		t.Fatalf("unexpected invalid requirement result: %+v", result)
	}
}
