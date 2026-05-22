package agent

import (
	"strings"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
)

func TestSelectWorkerAgentChoosesCodingForImplementationTask(t *testing.T) {
	agents := testAgents()

	decision := selectWorkerAgent(agents, "修复前端 React 组件里的 bug，并补充 Go API 测试", "1. Inspect frontend state\n2. Patch backend API\n3. Run tests")

	if decision.Agent.ID != "agent_coding" {
		t.Fatalf("expected coding agent, got %s with output:\n%s", decision.Agent.ID, decision.Output())
	}
	if !strings.Contains(decision.Output(), "Candidate scores") {
		t.Fatalf("expected transparent candidate scores, got:\n%s", decision.Output())
	}
}

func TestSelectWorkerAgentChoosesResearchForMarketTask(t *testing.T) {
	agents := testAgents()

	decision := selectWorkerAgent(agents, "Compare competitors and verify recent pricing sources for this product launch", "1. Gather sources\n2. Compare market positioning")

	if decision.Agent.ID != "agent_research" {
		t.Fatalf("expected research agent, got %s with output:\n%s", decision.Agent.ID, decision.Output())
	}
}

func TestSelectWorkerAgentChoosesDataForBudgetTask(t *testing.T) {
	agents := testAgents()

	decision := selectWorkerAgent(agents, "计算下个季度预算、成本和 capacity tradeoff", "1. Estimate cost\n2. Compare budget scenarios")

	if decision.Agent.ID != "agent_data" {
		t.Fatalf("expected data agent, got %s with output:\n%s", decision.Agent.ID, decision.Output())
	}
}

func TestParseLLMRouteDecision(t *testing.T) {
	agents := testAgents()

	decision, err := parseLLMRouteDecision(`{
		"agent_id": "agent_research",
		"reason": "The task depends on market context and external source comparison.",
		"confidence": 0.82,
		"scores": [
			{"agent_id": "agent_research", "score": 91, "reason": "Best fit for source comparison."},
			{"agent_id": "agent_coding", "score": 22, "reason": "Some implementation detail, but not primary."}
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
		t.Fatalf("expected one score per agent after fill, got %d", len(decision.Scores))
	}
}

func TestParseLLMRouteDecisionRejectsUnknownAgent(t *testing.T) {
	_, err := parseLLMRouteDecision(`{"agent_id":"agent_missing","reason":"bad","scores":[]}`, testAgents())
	if err == nil {
		t.Fatal("expected unknown agent error")
	}
}

func testAgents() []domain.Agent {
	now := time.Now().UTC()
	return []domain.Agent{
		{
			ID:           "agent_research",
			Name:         "Field Researcher",
			Description:  "Investigates people, places, products, and market context, then separates verified facts from open questions.",
			SystemPrompt: "Gather research context with search and compare sources carefully.",
			CreatedAt:    now,
			UpdatedAt:    now,
		},
		{
			ID:           "agent_coding",
			Name:         "Systems Builder",
			Description:  "Turns implementation requests into concrete technical steps, debugging hypotheses, and maintainable code changes.",
			SystemPrompt: "Focus on software behavior, interfaces, edge cases, and implementation tradeoffs.",
			CreatedAt:    now,
			UpdatedAt:    now,
		},
		{
			ID:           "agent_data",
			Name:         "Operations Analyst",
			Description:  "Evaluates budgets, schedules, capacity, and tradeoffs with explicit assumptions and calculation-backed reasoning.",
			SystemPrompt: "Treat questions as operational decisions involving cost, time, capacity, or prioritization.",
			CreatedAt:    now,
			UpdatedAt:    now,
		},
		{
			ID:           "agent_planner",
			Name:         "Narrative Strategist",
			Description:  "Shapes messy goals into audience-aware briefs, storylines, launch plans, and decision-ready next actions.",
			SystemPrompt: "Clarify the audience, intent, and constraints behind a request.",
			CreatedAt:    now,
			UpdatedAt:    now,
		},
	}
}
