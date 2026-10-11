package agent

import (
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/tool"
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestSelectWorkerAgentChoosesCodingForImplementationTask(t *testing.T) {
	agents := testAgents()

	decision := rankWorkerAgentsDeclarative(agents, "修复前端 React 组件里的 bug，并补充 Go API 测试", "1. Inspect frontend state\n2. Patch backend API\n3. Run tests", domain.AgentRoutingRequirements{})

	if decision.Agent.ID != "agent_coding" {
		t.Fatalf("expected coding agent, got %s with output:\n%s", decision.Agent.ID, decision.Output())
	}
	if !strings.Contains(decision.Output(), "Candidate scores") {
		t.Fatalf("expected transparent candidate scores, got:\n%s", decision.Output())
	}
}

func TestSelectWorkerAgentChoosesResearchForMarketTask(t *testing.T) {
	agents := testAgents()

	decision := rankWorkerAgentsDeclarative(agents, "Compare competitors and verify recent pricing sources for this product launch", "1. Gather sources\n2. Compare market positioning", domain.AgentRoutingRequirements{})

	if decision.Agent.ID != "agent_research" {
		t.Fatalf("expected research agent, got %s with output:\n%s", decision.Agent.ID, decision.Output())
	}
}

func TestSelectWorkerAgentChoosesDataForBudgetTask(t *testing.T) {
	agents := testAgents()

	decision := rankWorkerAgentsDeclarative(agents, "计算下个季度预算、成本和 capacity tradeoff", "1. Estimate cost\n2. Compare budget scenarios", domain.AgentRoutingRequirements{})

	if decision.Agent.ID != "agent_data" {
		t.Fatalf("expected data agent, got %s with output:\n%s", decision.Agent.ID, decision.Output())
	}
}

func TestDeclarativeRankingAppliesExclusions(t *testing.T) {
	agents := []domain.Agent{
		{ID: "medical", Name: "Market Researcher", RoutingHints: domain.AgentRoutingHints{Capabilities: []string{"market"}, Exclusions: []string{"medical diagnosis"}}},
		{ID: "analyst", Name: "General Analyst", RoutingHints: domain.AgentRoutingHints{Capabilities: []string{"analysis"}}},
	}
	decision := rankWorkerAgentsDeclarative(agents, "Analyze the market for medical diagnosis tools", "Provide analysis", domain.AgentRoutingRequirements{})
	if decision.Agent.ID != "analyst" {
		t.Fatalf("expected exclusion to demote medical agent, got decision=%#v", decision)
	}
}

func TestDeclarativeRankingDoesNotSubstringMatchShortEnglishHints(t *testing.T) {
	agents := []domain.Agent{{
		ID: "coding", Name: "Coding Agent",
		RoutingHints: domain.AgentRoutingHints{Capabilities: []string{"go"}},
	}}
	decision := rankWorkerAgentsDeclarative(agents, "Set quarterly goals", "Draft objectives", domain.AgentRoutingRequirements{})
	if decision.Scores[0].Score != 0 {
		t.Fatalf("short hint must match a complete token: decision=%#v", decision)
	}
}

func TestDeclarativeRankingUsesSoftPreferencesOnlyForRanking(t *testing.T) {
	agents := []domain.Agent{
		{ID: "coding", Name: "Coding", RoutingHints: domain.AgentRoutingHints{Capabilities: []string{"analysis", "coding"}}},
		{ID: "research", Name: "Research", RoutingHints: domain.AgentRoutingHints{Capabilities: []string{"analysis", "research"}}},
	}
	decision := rankWorkerAgentsDeclarative(agents, "Analyze the options", "Compare evidence", domain.AgentRoutingRequirements{
		PreferredCapabilities: []string{"research"},
	})
	if decision.Agent.ID != "research" || !strings.Contains(decision.Reason, "preferred capabilities") {
		t.Fatalf("soft preference did not influence ranking: decision=%#v", decision)
	}
}

func TestEligibleWorkerAgentsAppliesTypedHardRequirements(t *testing.T) {
	agents := []domain.Agent{
		{ID: "qualified", Tools: []string{"calculator"}, MemoryEnabled: true, RetrievalEnabled: true},
		{ID: "missing", Tools: []string{"get_current_time"}},
	}
	requirements := domain.AgentRoutingRequirements{
		RequiredTools: []string{"calculator"}, ProhibitedTools: []string{"get_current_time"},
		RequireMemory: true, RequireRetrieval: true,
	}
	eligible, evidence := eligibleWorkerAgents(agents, tool.DefaultCatalog(), requirements)
	if len(eligible) != 1 || eligible[0].ID != "qualified" {
		t.Fatalf("eligible agents = %#v", eligible)
	}
	if evidence[0].RequirementCoverage != 1 || len(evidence[0].MatchedRequirements) != 4 {
		t.Fatalf("qualified evidence = %#v", evidence[0])
	}
	if evidence[1].RequirementCoverage != 0 || len(evidence[1].ExclusionReasons) != 4 {
		t.Fatalf("missing requirement evidence = %#v", evidence[1])
	}
}

func TestOptionalAgentToolDoesNotBlockRoutingWhenDisabled(t *testing.T) {
	cfg := tool.DefaultConfig()
	cfg.EnabledTools = []string{"calculator", "get_current_time"}
	catalog, err := tool.BuildCatalog(cfg)
	if err != nil {
		t.Fatal(err)
	}
	agents := []domain.Agent{{ID: "research", Tools: []string{"web_search"}}}
	eligible, evidence := eligibleWorkerAgents(agents, catalog, domain.AgentRoutingRequirements{})
	if len(eligible) != 1 || !evidence[0].Eligible {
		t.Fatalf("optional disabled tool excluded agent: eligible=%#v evidence=%#v", eligible, evidence)
	}
	if len(eligible[0].Tools) != 0 || len(evidence[0].Agent.Tools) != 1 {
		t.Fatalf("router must see effective Tools without changing configured evidence: eligible=%#v evidence=%#v", eligible, evidence)
	}
	eligible, evidence = eligibleWorkerAgents(agents, catalog, domain.AgentRoutingRequirements{RequiredTools: []string{"web_search"}})
	if len(eligible) != 0 || evidence[0].Eligible {
		t.Fatalf("required disabled tool did not exclude agent: eligible=%#v evidence=%#v", eligible, evidence)
	}
	eligible, evidence = eligibleWorkerAgents(agents, catalog, domain.AgentRoutingRequirements{ProhibitedTools: []string{"web_search"}})
	if len(eligible) != 1 || !evidence[0].Eligible {
		t.Fatalf("disabled tool should not violate prohibition: eligible=%#v evidence=%#v", eligible, evidence)
	}
}

func TestSelectionGateAbstainsOnAmbiguityAndLowConfidence(t *testing.T) {
	agents := []domain.Agent{{ID: "first"}, {ID: "second"}}
	tests := []struct {
		name     string
		decision routeDecision
		code     string
	}{
		{name: "low score", decision: routeDecision{Agent: agents[0], Score: 5, Scores: []agentScore{{Agent: agents[0], Score: 5}}}, code: "minimum_score_not_met"},
		{name: "tie", decision: routeDecision{Agent: agents[0], Score: 10, Scores: []agentScore{{Agent: agents[0], Score: 10}, {Agent: agents[1], Score: 10}}}, code: "minimum_score_margin_not_met"},
		{name: "low llm confidence", decision: routeDecision{Agent: agents[0], Mode: "llm", Score: 80, Confidence: 0.49, Scores: []agentScore{{Agent: agents[0], Score: 80}, {Agent: agents[1], Score: 40}}}, code: "minimum_llm_confidence_not_met"},
		{name: "missing requirement evidence", decision: routeDecision{Agent: agents[0], Score: 80, Scores: []agentScore{{Agent: agents[0], Score: 80}}}, code: "minimum_requirement_coverage_not_met"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			decision := test.decision
			eligibility := []agentEligibility{{Agent: agents[1], Eligible: true, RequirementCoverage: 1}}
			if test.name != "missing requirement evidence" {
				eligibility = append(eligibility, agentEligibility{Agent: agents[0], Eligible: true, RequirementCoverage: 1})
			}
			if err := applySelectionGate(&decision, eligibility); !errors.Is(err, ErrNoSuitableAgent) || !containsString(decision.Gate.ReasonCodes, test.code) {
				t.Fatalf("gate decision=%#v err=%v", decision, err)
			}
			if decision.Agent.ID != "" || decision.Gate.ProposedAgentID != "first" {
				t.Fatalf("abstention confused proposed and selected agents: %#v", decision)
			}
		})
	}
}

func TestSelectionGateAcceptsQualifiedClearWinner(t *testing.T) {
	agent := domain.Agent{ID: "qualified"}
	decision := routeDecision{Agent: agent, Score: 8, Scores: []agentScore{{Agent: agent, Score: 8}}}
	eligibility := []agentEligibility{{Agent: agent, Eligible: true, RequirementCoverage: 1}}
	if err := applySelectionGate(&decision, eligibility); err != nil {
		t.Fatalf("qualified route was rejected: decision=%#v err=%v", decision, err)
	}
	if decision.Gate.ThresholdSource != "conservative-safety-baseline-v1" {
		t.Fatalf("threshold evidence missing: %#v", decision.Gate)
	}
}

func TestRouteWorkerAgentRejectsConflictingRequirements(t *testing.T) {
	runtime := &Runtime{}
	decision, err := runtime.routeWorkerAgent(context.Background(), "run_conflict", restoredRuntime{
		routerMode: RouterModeQuery, catalog: tool.DefaultCatalog(),
	}, testAgents(), "calculate", "use calculator", domain.AgentRoutingRequirements{
		RequiredTools: []string{"calculator"}, ProhibitedTools: []string{"calculator"},
	})
	if !errors.Is(err, ErrInvalidRoutingRequirements) || decision.Outcome != AgentSelectionOutcomeRouterFailed || decision.FailureCode != "agent_route_requirements_invalid" {
		t.Fatalf("conflicting requirements decision=%#v err=%v", decision, err)
	}
}

func TestAgentSelectionReturnsTypedNoSuitableOutcome(t *testing.T) {
	runtime := &Runtime{}
	decision, err := runtime.routeWorkerAgent(context.Background(), "run_v2", restoredRuntime{
		routerMode: RouterModeQuery, catalog: tool.DefaultCatalog(),
	}, []domain.Agent{{ID: "invoices", Name: "Invoice Clerk", Description: "Reconciles invoices."}}, "write a sonnet", "use imagery", domain.AgentRoutingRequirements{})
	if !errors.Is(err, ErrNoSuitableAgent) || decision.Outcome != AgentSelectionOutcomeNoSuitable || decision.FailureCode != "agent_route_no_suitable_candidate" {
		t.Fatalf("unexpected no-suitable route: decision=%#v err=%v", decision, err)
	}
}

func TestEligibleWorkerAgentsRequiresStableIdentity(t *testing.T) {
	agents := []domain.Agent{
		{ID: "eligible", Tools: []string{"calculator"}},
		{ID: "missing-tool", Tools: []string{"not_frozen"}},
		{ID: "duplicate"},
		{ID: "duplicate"},
		{ID: " "},
	}
	eligible, evidence := eligibleWorkerAgents(agents, tool.DefaultCatalog(), domain.AgentRoutingRequirements{})
	if len(eligible) != 2 || eligible[0].ID != "eligible" || eligible[1].ID != "missing-tool" {
		t.Fatalf("eligible agents = %#v", eligible)
	}
	joined := ""
	for _, item := range evidence {
		joined += strings.Join(item.ExclusionReasons, ",")
	}
	for _, reason := range []string{"agent_id_duplicate", "agent_id_missing"} {
		if !strings.Contains(joined, reason) {
			t.Fatalf("missing exclusion reason %q in %#v", reason, evidence)
		}
	}
}

func testAgents() []domain.Agent {
	return store.DefaultAgents(time.Now().UTC())
}
