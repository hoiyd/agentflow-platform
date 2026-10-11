package agent

import (
	"agentflow-platform/apps/api/internal/failure"
	"context"
	"testing"
)

func TestParseLLMRouteDecision(t *testing.T) {
	agents := testAgents()

	decision, err := parseLLMRouteDecision(`{
		"agent_id": "agent_research",
		"reason": "The task depends on market context and external source comparison.",
		"confidence": 0.82,
		"scores": [
			{"agent_id": "agent_research", "score": 91, "reason": "Best fit for source comparison."},
			{"agent_id": "agent_coding", "score": 22, "reason": "Some implementation detail, but not primary."},
			{"agent_id": "agent_data", "score": 18, "reason": "No quantitative analysis is required."},
			{"agent_id": "agent_planner", "score": 30, "reason": "Planning is secondary to research."}
		]
	}`, agents)
	if err != nil {
		t.Fatalf("parse llm route decision: %v", err)
	}
	if decision.Agent.ID != "agent_research" {
		t.Fatalf("expected research agent, got %s", decision.Agent.ID)
	}
	if decision.Mode != "llm" || decision.Confidence != 0.82 {
		t.Fatalf("expected llm mode and confidence, got %#v", decision)
	}
	if len(decision.Scores) != len(agents) {
		t.Fatalf("expected one score per agent, got %d", len(decision.Scores))
	}
}

func TestParseLLMRouteDecisionRejectsUnknownAgent(t *testing.T) {
	_, err := parseLLMRouteDecision(`{"agent_id":"agent_missing","reason":"bad","scores":[]}`, testAgents())
	if err == nil {
		t.Fatal("expected unknown agent error")
	}
}

func TestParseLLMRouteDecisionRejectsIncompleteOrInconsistentScores(t *testing.T) {
	agents := testAgents()[:2]
	tests := []string{
		`{"agent_id":"agent_research","reason":"fit","confidence":0.8,"scores":[{"agent_id":"agent_research","score":90,"reason":"fit"}]}`,
		`{"agent_id":"agent_research","reason":"fit","confidence":0.8,"scores":[{"agent_id":"agent_research","score":90,"reason":"fit"},{"agent_id":"agent_research","score":80,"reason":"duplicate"}]}`,
		`{"agent_id":"agent_research","reason":"fit","confidence":1.2,"scores":[{"agent_id":"agent_research","score":90,"reason":"fit"},{"agent_id":"agent_coding","score":20,"reason":"weak"}]}`,
		`{"agent_id":"agent_coding","reason":"fit","confidence":0.8,"scores":[{"agent_id":"agent_research","score":90,"reason":"fit"},{"agent_id":"agent_coding","score":20,"reason":"weak"}]}`,
		`{"agent_id":"agent_research","reason":"fit","confidence":0.8,"scores":[{"agent_id":"agent_research","score":101,"reason":"fit"},{"agent_id":"agent_coding","score":20,"reason":"weak"}]}`,
		`{"agent_id":"agent_research","reason":"fit","confidence":0.8,"scores":[{"agent_id":"agent_research","score":90,"reason":""},{"agent_id":"agent_coding","score":20,"reason":"weak"}]}`,
	}
	for _, response := range tests {
		if _, err := parseLLMRouteDecision(response, agents); err == nil {
			t.Fatalf("expected invalid router response to fail: %s", response)
		}
	}
}

func TestAgentSelectionFallbackOnlyHandlesTransientOrInvalidModelResults(t *testing.T) {
	transient := failure.New(failure.Definition{Message: "temporarily unavailable", Info: failure.Info{
		Code: "provider_unavailable", Source: "model_provider", Category: failure.CategoryAvailability, Retryable: true,
	}})
	auth := failure.New(failure.Definition{Message: "invalid credential", Info: failure.Info{
		Code: "authentication", Source: "model_provider", Category: failure.CategoryAuthentication,
	}})
	budget := failure.New(failure.Definition{Message: "budget exhausted", Info: failure.Info{
		Code: "budget_exceeded", Source: "run_budget", Category: failure.CategoryCapacity,
	}})
	if !shouldFallbackAgentSelection(transient) || !shouldFallbackAgentSelection(ErrAgentRouteResponseInvalid) {
		t.Fatal("transient and invalid router responses must use the deterministic baseline")
	}
	for _, err := range []error{auth, budget, context.Canceled} {
		if shouldFallbackAgentSelection(err) {
			t.Fatalf("terminal failure must not be hidden by fallback: %v", err)
		}
	}
}
