package agent

import "agentflow-platform/apps/api/internal/testsupport/fixturestore"

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	eventpkg "agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/failure"
	"agentflow-platform/apps/api/internal/inference/provider"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/taskstate"
	"agentflow-platform/apps/api/internal/tool"
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

func TestAgentSelectionReturnsTypedNoSuitableOutcome(t *testing.T) {
	runtime := &Runtime{}
	decision, err := runtime.routeWorkerAgent(context.Background(), "run_v2", restoredRuntime{
		routerMode: RouterModeQuery, catalog: tool.DefaultCatalog(),
	}, []domain.Agent{{ID: "invoices", Name: "Invoice Clerk", Description: "Reconciles invoices."}}, "write a sonnet", "use imagery", domain.AgentRoutingRequirements{})
	if !errors.Is(err, ErrNoSuitableAgent) || decision.Outcome != AgentSelectionOutcomeNoSuitable || decision.FailureCode != "agent_route_no_suitable_candidate" {
		t.Fatalf("unexpected no-suitable route: decision=%#v err=%v", decision, err)
	}
}

func TestEligibleWorkerAgentsRequiresStableIdentityAndFrozenTools(t *testing.T) {
	agents := []domain.Agent{
		{ID: "eligible", Tools: []string{"calculator"}},
		{ID: "missing-tool", Tools: []string{"not_frozen"}},
		{ID: "duplicate"},
		{ID: "duplicate"},
		{ID: " "},
	}
	eligible, evidence := eligibleWorkerAgents(agents, tool.DefaultCatalog(), domain.AgentRoutingRequirements{})
	if len(eligible) != 1 || eligible[0].ID != "eligible" {
		t.Fatalf("eligible agents = %#v", eligible)
	}
	joined := ""
	for _, item := range evidence {
		joined += strings.Join(item.ExclusionReasons, ",")
	}
	for _, reason := range []string{"tool_unavailable:not_frozen", "agent_id_duplicate", "agent_id_missing"} {
		if !strings.Contains(joined, reason) {
			t.Fatalf("missing exclusion reason %q in %#v", reason, evidence)
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

func TestPreparedRunsUseRequestedAgent(t *testing.T) {
	fixtureStore := fixturestore.New()

	runtime := NewRuntime(RuntimeOptions{Store: fixtureStore, ModelClient: newLocalFallbackOpenAIClientForTest()})
	custom, err := fixtureStore.CreateAgent(domain.Agent{
		Name:             "Resume Reviewer",
		Description:      "Reviews resumes against job descriptions.",
		SystemPrompt:     "Review resume evidence.",
		MemoryEnabled:    true,
		RetrievalEnabled: true,
		Executor:         domain.DefaultAgentExecutor,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	conversation, err := fixtureStore.CreateConversation("Custom agent run")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}

	collaboration, err := runtime.PrepareCollaborationRunWithContract(context.Background(), custom.ID, conversation.ID, nil)
	if err != nil {
		t.Fatalf("prepare collaboration: %v", err)
	}
	if collaboration.WorkerAgent.ID != custom.ID || collaboration.Run.AgentID != custom.ID {
		t.Fatalf("expected collaboration to use requested agent, got agent=%s run_agent=%s", collaboration.WorkerAgent.ID, collaboration.Run.AgentID)
	}
	autonomous, err := runtime.PrepareAutonomousRunWithContract(context.Background(), custom.ID, conversation.ID, nil)
	if err != nil {
		t.Fatalf("prepare autonomous: %v", err)
	}
	if autonomous.WorkerAgent.ID != custom.ID || autonomous.Run.AgentID != custom.ID {
		t.Fatalf("expected autonomous to use requested agent, got agent=%s run_agent=%s", autonomous.WorkerAgent.ID, autonomous.Run.AgentID)
	}
}

func TestMultiAgentWorkerRunsAsIsolatedParentStage(t *testing.T) {
	fixtureStore := fixturestore.New()
	conversation, err := fixtureStore.CreateConversation("stage-based collaboration")
	if err != nil {
		t.Fatal(err)
	}
	retriever := &recordingKnowledgeRetriever{}
	runtime := NewRuntime(RuntimeOptions{
		Store: fixtureStore, ModelClient: newLocalFallbackOpenAIClientForTest(), RouterMode: RouterModeQuery,
		KnowledgeRetriever: retriever,
	})
	prepared, err := runtime.PrepareCollaborationRunWithContract(context.Background(), "agent_planner", conversation.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	const task = "Implement and test a Go API change"
	events, errs := runtime.RunCollaboration(context.Background(), prepared, task)
	for range events {
	}
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
	events, errs = runtime.ContinueCollaboration(context.Background(), prepared.Run.ID, "Inspect, implement, and test the change.", domain.AgentRoutingRequirements{})
	for range events {
	}
	if err := <-errs; err != nil {
		t.Fatal(err)
	}

	runs, err := fixtureStore.ListRuns()
	if err != nil || len(runs) != 1 {
		t.Fatalf("multi-agent execution created extra runs: count=%d err=%v", len(runs), err)
	}
	steps, err := fixtureStore.ListCollaborationSteps(prepared.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"planner", "router", "worker", "reviewer", "finalizer"} {
		step, found := latestCompletedCollaborationStep(steps, role)
		if !found || step.RunID != prepared.Run.ID {
			t.Fatalf("expected completed parent %s stage, got %#v", role, step)
		}
	}
	for _, item := range steps {
		if strings.Contains(item.Output, "Child trace:") {
			t.Fatalf("legacy child reference leaked into stage %s", item.ID)
		}
	}
	assertRuntimeRetrievalBoundary(t, fixtureStore, prepared.Run.ID, task, retriever.searches)
}

func TestWorkerStageToolAndContextBoundary(t *testing.T) {
	catalog := tool.DefaultCatalog()
	agent := domain.Agent{Tools: []string{"get_current_time", taskstate.UpdateToolName, "not-installed"}}
	isolated, err := catalogForAgent(catalog, agent)
	if err != nil {
		t.Fatal(err)
	}
	if names := isolated.EnabledNames(); len(names) != 1 || names[0] != "get_current_time" {
		t.Fatalf("unexpected isolated tool set: %#v", names)
	}

	full := strings.Repeat("x", workerHandoffMaxCharacters+100)
	step := domain.CollaborationStep{RunID: "run-parent", ID: "stage-worker", Output: full}
	handoff := boundedWorkerHandoff(step)
	if len([]rune(handoff)) > workerHandoffMaxCharacters || !strings.Contains(handoff, "run://run-parent/stages/stage-worker") {
		t.Fatalf("worker handoff was not bounded with a durable stage reference: %q", handoff[len(handoff)-80:])
	}
	if step.Output != full {
		t.Fatal("bounding the handoff changed the durable stage output")
	}
}

type blockingPreparedClient struct {
	provider.Client
	started chan struct{}
}

func (c *blockingPreparedClient) WithRuntimeIdentity(identity provider.RuntimeIdentity) provider.Client {
	return &blockingPreparedClient{Client: c.Client.WithRuntimeIdentity(identity), started: c.started}
}

func (c *blockingPreparedClient) CompletePreparedText(ctx context.Context, _ provider.PreparedText) (provider.TextCompletion, error) {
	select {
	case c.started <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return provider.TextCompletion{}, ctx.Err()
}

func (c *blockingPreparedClient) StreamAnswer(ctx context.Context, _ provider.PreparedChat, _ provider.ChatStreamKind, _ provider.ChatTrace, _ chan<- provider.StreamEvent) (bool, error) {
	select {
	case c.started <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return false, ctx.Err()
}

func TestCancelRunStopsActiveWorkerStage(t *testing.T) {
	fixtureStore := fixturestore.New()
	conversation, err := fixtureStore.CreateConversation("cancel isolated worker")
	if err != nil {
		t.Fatal(err)
	}
	client := &blockingPreparedClient{Client: newLocalFallbackOpenAIClientForTest(), started: make(chan struct{}, 1)}
	runtime := NewRuntime(RuntimeOptions{Store: fixtureStore, ModelClient: client, RouterMode: RouterModeQuery})
	prepared, err := runtime.PrepareCollaborationRunWithContract(context.Background(), "agent_planner", conversation.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	createCompletedStage(t, runtime, prepared.Run, "planner", "agent_planner", "task", "approved plan")
	if _, err := fixtureStore.UpdateRunStatus(prepared.Run.ID, domain.RunWaitingForUser, ""); err != nil {
		t.Fatal(err)
	}

	events, errs := runtime.ContinueCollaboration(context.Background(), prepared.Run.ID, "approved plan", domain.AgentRoutingRequirements{})
	done := make(chan error, 1)
	go func() {
		for range events {
		}
		done <- <-errs
	}()
	select {
	case <-client.started:
	case <-time.After(time.Second):
		t.Fatal("worker stage did not start")
	}
	if _, err := runtime.CancelRun(prepared.Run.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context cancellation, got %v", err)
		}
		canceled, failErr := runtime.FailRun(prepared.Run.ID, err)
		if failErr != nil || canceled.Status != domain.RunCanceled {
			t.Fatalf("finalize canceled run: run=%#v err=%v", canceled, failErr)
		}
		steps, listErr := fixtureStore.ListCollaborationSteps(prepared.Run.ID)
		if listErr != nil {
			t.Fatal(listErr)
		}
		if _, found := latestCompletedCollaborationStep(steps, "reviewer"); found {
			t.Fatal("reviewer ran after the worker was canceled")
		}
	case <-time.After(time.Second):
		t.Fatal("active worker ignored cancellation")
	}
}

func TestCancelRunStopsActiveSingleTurn(t *testing.T) {
	fixtureStore := fixturestore.New()
	conversation, err := fixtureStore.CreateConversation("cancel single turn")
	if err != nil {
		t.Fatal(err)
	}
	client := &blockingPreparedClient{Client: newLocalFallbackOpenAIClientForTest(), started: make(chan struct{}, 1)}
	runtime := NewRuntime(RuntimeOptions{Store: fixtureStore, ModelClient: client})
	prepared, err := runtime.PrepareChatRunWithContract(context.Background(), "agent_planner", conversation.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	events, errs := runtime.StreamChat(context.Background(), prepared, nil, "wait")
	done := make(chan error, 1)
	go func() {
		for range events {
		}
		done <- <-errs
	}()
	select {
	case <-client.started:
	case <-time.After(time.Second):
		t.Fatal("single turn did not start")
	}
	if _, err := runtime.CancelRun(prepared.Run.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context cancellation, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("single turn ignored cancellation")
	}
}

func TestCancelRunStopsActivePlannerStage(t *testing.T) {
	fixtureStore := fixturestore.New()
	conversation, err := fixtureStore.CreateConversation("cancel planner")
	if err != nil {
		t.Fatal(err)
	}
	client := &blockingPreparedClient{Client: newLocalFallbackOpenAIClientForTest(), started: make(chan struct{}, 1)}
	runtime := NewRuntime(RuntimeOptions{Store: fixtureStore, ModelClient: client})
	prepared, err := runtime.PrepareCollaborationRunWithContract(context.Background(), "agent_planner", conversation.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	events, errs := runtime.RunCollaboration(context.Background(), prepared, "wait")
	done := make(chan error, 1)
	go func() {
		for range events {
		}
		done <- <-errs
	}()
	select {
	case <-client.started:
	case <-time.After(time.Second):
		t.Fatal("planner stage did not start")
	}
	if _, err := runtime.CancelRun(prepared.Run.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context cancellation, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("planner stage ignored cancellation")
	}
}

func TestResumeRecoverableCollaborationReusesCompletedStages(t *testing.T) {
	runtime, fixtureStore, run := newStageRecoverableCollaboration(t)
	createCompletedStage(t, runtime, run, "planner", "agent_planner", "task", "plan")
	createCompletedStage(t, runtime, run, "router", "agent_planner", "route", "agent_planner")
	createCompletedStage(t, runtime, run, "worker", "agent_planner", "work", "durable worker output")
	createCompletedStage(t, runtime, run, "reviewer", "", "review", "durable review")

	events, errs := runtime.ResumeRecoverableCollaboration(context.Background(), run.ID)
	for range events {
	}
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
	steps, err := fixtureStore.ListCollaborationSteps(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, step := range steps {
		counts[step.Role]++
	}
	if counts["worker"] != 1 || counts["reviewer"] != 1 || counts["finalizer"] != 1 {
		t.Fatalf("resume duplicated committed stages: %#v", counts)
	}
}

func TestResumeRecoverableCollaborationRequiresCompletedBoundary(t *testing.T) {
	runtime, _, run := newStageRecoverableCollaboration(t)
	events, errs := runtime.ResumeRecoverableCollaboration(context.Background(), run.ID)
	for range events {
	}
	if err := <-errs; err == nil || !strings.Contains(err.Error(), "completed planner step not found") {
		t.Fatalf("unexpected recovery error: %v", err)
	}
}

func newStageRecoverableCollaboration(t *testing.T) (*Runtime, *fixturestore.Store, domain.Run) {
	t.Helper()
	fixtureStore := fixturestore.New()
	conversation, err := fixtureStore.CreateConversation("recoverable stage collaboration")
	if err != nil {
		t.Fatal(err)
	}
	runtime := NewRuntime(RuntimeOptions{
		Store: fixtureStore, ModelClient: newLocalFallbackOpenAIClientForTest(), RouterMode: RouterModeQuery,
	})
	prepared, err := runtime.PrepareCollaborationRunWithContract(context.Background(), "agent_planner", conversation.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	run, err := fixtureStore.UpdateRunStatus(prepared.Run.ID, domain.RunFailedRecoverable, "worker interrupted")
	if err != nil {
		t.Fatal(err)
	}
	return runtime, fixtureStore, run
}

func createCompletedStage(t *testing.T, runtime *Runtime, run domain.Run, role, agentID, input, output string) {
	t.Helper()
	step, err := runtime.store.CreateCollaborationStep(domain.CollaborationStep{
		RunID: run.ID, ConversationID: run.ConversationID, Role: role, AgentID: agentID,
		Status: domain.CollaborationStepRunning, Input: input,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.publishStage(context.Background(), step, domain.EventStageStarted); err != nil {
		t.Fatal(err)
	}
	step, err = runtime.store.UpdateCollaborationStep(step.ID, domain.CollaborationStepCompleted, output, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.publishStage(context.Background(), step, domain.EventStageCompleted); err != nil {
		t.Fatal(err)
	}
}

func TestParseAutonomousDecision(t *testing.T) {
	decision := parseAutonomousDecision(`{"decision":"stop","reason":"done","final_answer":"Complete."}`)
	if !decision.ValidJSON {
		t.Fatal("expected valid decision JSON")
	}
	if decision.Decision != "stop" || decision.Reason != "done" || decision.FinalAnswer != "Complete." {
		t.Fatalf("unexpected decision: %#v", decision)
	}
}

func TestParseAutonomousDecisionAskUser(t *testing.T) {
	decision := parseAutonomousDecision(`{"decision":"ask_user","reason":"missing project name","question":"What project should this status update describe?","final_answer":""}`)
	if !decision.ValidJSON {
		t.Fatal("expected valid decision JSON")
	}
	if decision.Decision != "ask_user" || decision.Question == "" {
		t.Fatalf("unexpected ask_user decision: %#v", decision)
	}
}

func TestInferHumanInputNeedFromPlan(t *testing.T) {
	need := inferHumanInputNeed(
		"Task is understood.",
		"Plan:\n1. Cannot proceed without user input about the target customer segment before drafting the launch plan.",
		"Not started.",
		"Review pending.",
		`{"decision":"stop","reason":"done","final_answer":"Complete."}`,
	)
	if !need.Needed {
		t.Fatal("expected human input need")
	}
	if need.Source != "plan" {
		t.Fatalf("expected plan source, got %q", need.Source)
	}
	if !strings.Contains(need.Question, "target customer segment") {
		t.Fatalf("expected generated question to include evidence, got %q", need.Question)
	}
}

func TestInferHumanInputNeedFromChineseReview(t *testing.T) {
	need := inferHumanInputNeed(
		"任务已理解。",
		"先整理已有信息。",
		"已完成初稿。",
		"Review:\n- 需要用户补充目标受众，否则无法继续完善文案。",
		`{"decision":"continue","reason":"next iteration"}`,
	)
	if !need.Needed {
		t.Fatal("expected human input need")
	}
	if need.Source != "review" {
		t.Fatalf("expected review source, got %q", need.Source)
	}
	if !strings.Contains(need.Question, "目标受众") {
		t.Fatalf("expected generated question to include Chinese evidence, got %q", need.Question)
	}
}

func TestInferHumanInputNeedIgnoresNegativeStatement(t *testing.T) {
	need := inferHumanInputNeed(
		"Task is understood.",
		"No additional user input is required; continue with the given constraints.",
		"Draft is complete.",
		"Review: no blocking gaps.",
		`{"decision":"stop","reason":"done","final_answer":"Complete."}`,
	)
	if need.Needed {
		t.Fatalf("did not expect human input need: %#v", need)
	}
}

func TestParseAutonomousDecisionRejectsBadJSON(t *testing.T) {
	decision := parseAutonomousDecision("not json")
	if decision.ValidJSON || decision.Decision != "" {
		t.Fatalf("expected invalid empty decision, got %#v", decision)
	}
}

func TestAutonomousRunStopsAtMaxIterations(t *testing.T) {
	fixtureStore := fixturestore.New()

	conversation, err := fixtureStore.CreateConversation("Autonomous test")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	retriever := &recordingKnowledgeRetriever{}
	runtime := NewRuntime(RuntimeOptions{
		Store: fixtureStore, ModelClient: newLocalFallbackOpenAIClientForTest(), RouterMode: RouterModeQuery,
		KnowledgeRetriever: retriever,
		Autonomous: AutonomousLimits{
			MaxIterations: 1, MaxRuntime: time.Minute, MaxOutputChars: 60000, MaxToolCalls: 20,
		},
	})
	prepared, err := runtime.PrepareAutonomousRunWithContract(context.Background(), "", conversation.ID, nil)
	if err != nil {
		t.Fatalf("prepare autonomous run: %v", err)
	}

	const task = "Write a concise project update."
	events, errs := runtime.RunAutonomous(context.Background(), prepared, task)
	seenProgress := false
	for event := range events {
		if event.Type == domain.EventRunProgress {
			seenProgress = true
		}
	}
	if err := <-errs; err != nil {
		t.Fatalf("run autonomous: %v", err)
	}
	if !seenProgress {
		t.Fatal("expected autonomous progress event")
	}

	steps, err := fixtureStore.ListCollaborationSteps(prepared.Run.ID)
	if err != nil {
		t.Fatalf("list steps: %v", err)
	}
	if len(steps) == 0 {
		t.Fatal("expected autonomous steps")
	}
	if steps[0].Iteration != 1 {
		t.Fatalf("expected first step iteration 1, got %d", steps[0].Iteration)
	}
	if steps[len(steps)-1].Role != "final" {
		t.Fatalf("expected final step, got %q", steps[len(steps)-1].Role)
	}
	runEvents, err := fixtureStore.ListRunEvents(prepared.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := eventpkg.ValidateLifecycle(runEvents); err != nil {
		t.Fatalf("invalid autonomous event lifecycle: %v", err)
	}
	assertRuntimeRetrievalBoundary(t, fixtureStore, prepared.Run.ID, task, retriever.searches)
}

func assertRuntimeRetrievalBoundary(t *testing.T, fixtureStore *fixturestore.Store, runID, query string, searches []domain.DocumentSearch) {
	t.Helper()
	if len(searches) == 0 {
		t.Fatal("expected knowledge retrieval")
	}
	for _, search := range searches {
		if search.Query != query {
			t.Fatalf("stage scaffolding leaked into retrieval query: got %q want %q", search.Query, query)
		}
	}
	events, err := fixtureStore.ListRunEvents(runID)
	if err != nil {
		t.Fatal(err)
	}
	completed := 0
	for _, item := range events {
		if item.Type != domain.EventRetrievalCompleted {
			continue
		}
		completed++
		if item.StageID == "" || item.Payload["query"] != query || item.Payload["query_source"] != string(retrievalQuerySourceUserInput) {
			t.Fatalf("retrieval provenance does not identify the user query and owning stage: %#v", item)
		}
		if item.Payload["chunk_count"] != 0 {
			t.Fatalf("no-match retrieval leaked context: %#v", item.Payload)
		}
		if noMatch, present := item.Payload["rag_no_match"]; present && noMatch != true {
			t.Fatalf("unexpected no-match decision: %#v", item.Payload)
		}
		if sources, ok := item.Payload["citation_sources"].([]domain.RAGCitation); ok && len(sources) > 0 {
			t.Fatalf("no-match retrieval produced citation sources: %#v", item.Payload)
		}
	}
	if completed == 0 {
		t.Fatal("expected completed retrieval evidence")
	}
}

func TestAutonomousRunCanBeCanceledBeforeLoop(t *testing.T) {
	fixtureStore := fixturestore.New()

	conversation, err := fixtureStore.CreateConversation("Cancel test")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	runtime := NewRuntime(RuntimeOptions{
		Store: fixtureStore, ModelClient: newLocalFallbackOpenAIClientForTest(), RouterMode: RouterModeQuery,
		Autonomous: AutonomousLimits{
			MaxIterations: 2, MaxRuntime: time.Minute, MaxOutputChars: 60000, MaxToolCalls: 20,
		},
	})
	prepared, err := runtime.PrepareAutonomousRunWithContract(context.Background(), "", conversation.ID, nil)
	if err != nil {
		t.Fatalf("prepare autonomous run: %v", err)
	}
	if _, err := runtime.CancelRun(prepared.Run.ID); err != nil {
		t.Fatalf("cancel run: %v", err)
	}

	events, errs := runtime.RunAutonomous(context.Background(), prepared, "Long task")
	seenCanceled := false
	for event := range events {
		if event.Type == domain.EventRunCanceled && event.Payload["status"] == domain.RunCanceled {
			seenCanceled = true
		}
	}
	if err := <-errs; err != nil {
		t.Fatalf("expected clean cancel, got %v", err)
	}
	if !seenCanceled {
		t.Fatal("expected canceled run event")
	}
	run, ok, err := fixtureStore.GetRun(prepared.Run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if !ok || run.Status != domain.RunCanceled {
		t.Fatalf("expected canceled run, got %#v", run)
	}
}

func TestAutonomousCancelClosesActiveStage(t *testing.T) {
	fixtureStore := fixturestore.New()
	conversation, err := fixtureStore.CreateConversation("cancel active autonomous stage")
	if err != nil {
		t.Fatal(err)
	}
	client := &blockingPreparedClient{Client: newLocalFallbackOpenAIClientForTest(), started: make(chan struct{}, 1)}
	runtime := NewRuntime(RuntimeOptions{Store: fixtureStore, ModelClient: client, RouterMode: RouterModeQuery})
	prepared, err := runtime.PrepareAutonomousRunWithContract(context.Background(), "", conversation.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	events, errs := runtime.RunAutonomous(context.Background(), prepared, "wait")
	done := make(chan error, 1)
	go func() {
		for range events {
		}
		done <- <-errs
	}()
	select {
	case <-client.started:
	case <-time.After(time.Second):
		t.Fatal("autonomous model request did not start")
	}
	if _, err := runtime.CancelRun(prepared.Run.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("autonomous cancellation returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("autonomous cancellation did not finish")
	}
	runEvents, err := fixtureStore.ListRunEvents(prepared.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	var startedStage string
	var failedStage bool
	for _, event := range runEvents {
		switch event.Type {
		case domain.EventStageStarted:
			startedStage = event.StageID
		case domain.EventStageFailed:
			failedStage = event.StageID == startedStage
		}
	}
	if startedStage == "" || !failedStage {
		t.Fatalf("canceled autonomous stage has no terminal event: %#v", runEvents)
	}
}

func TestResumeAutonomousCompletesHumanInputCheckpoint(t *testing.T) {
	fixtureStore := fixturestore.New()

	conversation, err := fixtureStore.CreateConversation("HITL test")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	runtime := NewRuntime(RuntimeOptions{
		Store: fixtureStore, ModelClient: newLocalFallbackOpenAIClientForTest(), RouterMode: RouterModeQuery,
		Autonomous: AutonomousLimits{
			MaxIterations: 2, MaxRuntime: time.Minute, MaxOutputChars: 60000, MaxToolCalls: 20,
		},
	})
	prepared, err := runtime.PrepareAutonomousRunWithContract(context.Background(), "", conversation.ID, nil)
	if err != nil {
		t.Fatalf("prepare autonomous run: %v", err)
	}
	checkpoint, err := fixtureStore.CreateCollaborationStep(domain.CollaborationStep{
		RunID:          prepared.Run.ID,
		ConversationID: conversation.ID,
		Role:           "human_input",
		AgentID:        prepared.WorkerAgent.ID,
		Status:         domain.CollaborationStepRunning,
		Iteration:      1,
		Input:          "missing project",
		Output:         "Which project?",
	})
	if err != nil {
		t.Fatalf("create checkpoint: %v", err)
	}
	if _, err := fixtureStore.UpdateRunStatus(prepared.Run.ID, domain.RunWaitingForUser, ""); err != nil {
		t.Fatalf("mark waiting: %v", err)
	}

	events, errs := runtime.ResumeAutonomous(context.Background(), prepared.Run.ID, "AgentFlow")
	for range events {
	}
	if err := <-errs; err != nil {
		t.Fatalf("resume autonomous: %v", err)
	}
	updated, ok, err := fixtureStore.GetRun(prepared.Run.ID)
	if err != nil || !ok {
		t.Fatalf("get run after resume: %v", err)
	}
	if updated.Status == domain.RunWaitingForUser {
		t.Fatalf("expected run to leave waiting_for_user")
	}
	steps, err := fixtureStore.ListCollaborationSteps(prepared.Run.ID)
	if err != nil {
		t.Fatalf("list steps: %v", err)
	}
	foundCompletedCheckpoint := false
	for _, step := range steps {
		if step.ID == checkpoint.ID && step.Status == domain.CollaborationStepCompleted && step.Output == "AgentFlow" {
			foundCompletedCheckpoint = true
		}
	}
	if !foundCompletedCheckpoint {
		t.Fatal("expected completed human input checkpoint")
	}
}

func TestResumeRecoverableAutonomousContinuesFromSavedSteps(t *testing.T) {
	fixtureStore := fixturestore.New()

	conversation, err := fixtureStore.CreateConversation("Recovery resume test")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	runtime := NewRuntime(RuntimeOptions{
		Store: fixtureStore, ModelClient: newLocalFallbackOpenAIClientForTest(), RouterMode: RouterModeQuery,
		Autonomous: AutonomousLimits{
			MaxIterations: 2, MaxRuntime: time.Minute, MaxOutputChars: 60000, MaxToolCalls: 20,
		},
	})
	prepared, err := runtime.PrepareAutonomousRunWithContract(context.Background(), "", conversation.ID, nil)
	if err != nil {
		t.Fatalf("prepare autonomous run: %v", err)
	}
	if _, err := fixtureStore.CreateCollaborationStep(domain.CollaborationStep{
		RunID:          prepared.Run.ID,
		ConversationID: conversation.ID,
		Role:           "observe",
		AgentID:        prepared.WorkerAgent.ID,
		Status:         domain.CollaborationStepCompleted,
		Iteration:      1,
		Input:          "User task: Write a concise update.\n\nCurrent state: none",
		Output:         "Observed project context.",
	}); err != nil {
		t.Fatalf("create observe step: %v", err)
	}
	if _, err := fixtureStore.UpdateRunStatus(prepared.Run.ID, domain.RunFailedRecoverable, "heartbeat expired"); err != nil {
		t.Fatalf("mark recoverable: %v", err)
	}

	events, errs := runtime.ResumeRecoverableAutonomous(context.Background(), prepared.Run.ID, "Continue after restart")
	for range events {
	}
	if err := <-errs; err != nil {
		t.Fatalf("resume recoverable autonomous: %v", err)
	}
	updated, ok, err := fixtureStore.GetRun(prepared.Run.ID)
	if err != nil || !ok {
		t.Fatalf("get run after resume: %v", err)
	}
	if updated.Status == domain.RunFailedRecoverable {
		t.Fatalf("expected run to leave failed_recoverable")
	}
	steps, err := fixtureStore.ListCollaborationSteps(prepared.Run.ID)
	if err != nil {
		t.Fatalf("list steps: %v", err)
	}
	foundRecoveryStep := false
	for _, step := range steps {
		if step.Role == "recovery" && step.Output == "Continue after restart" {
			foundRecoveryStep = true
		}
	}
	if !foundRecoveryStep {
		t.Fatal("expected recovery collaboration step")
	}
}

func TestRecoverableStateIgnoresRecoveryStepForNextIteration(t *testing.T) {
	now := time.Now().UTC()
	state := rebuildRecoverableAutonomousState([]domain.CollaborationStep{
		{
			Role:      "observe",
			Status:    domain.CollaborationStepCompleted,
			Iteration: 1,
			Output:    "Observed context.",
			CreatedAt: now,
		},
	}, domain.CollaborationStep{
		Role:      "recovery",
		Status:    domain.CollaborationStepCompleted,
		Iteration: 2,
		Output:    "Resume after crash.",
		CreatedAt: now.Add(time.Second),
	})

	if state.NextIter != 2 {
		t.Fatalf("expected next iteration 2, got %d", state.NextIter)
	}
	if !strings.Contains(state.State, "Observed context.") {
		t.Fatalf("expected recovered state to include completed step output, got %q", state.State)
	}
}

func testAgents() []domain.Agent {
	return store.DefaultAgents(time.Now().UTC())
}
