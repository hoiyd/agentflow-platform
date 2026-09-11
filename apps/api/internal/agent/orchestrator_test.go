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
	"agentflow-platform/apps/api/internal/modelprovider"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/taskstate"
	"agentflow-platform/apps/api/internal/tools"
)

func TestSelectWorkerAgentChoosesCodingForImplementationTask(t *testing.T) {
	agents := testAgents()

	decision, err := selectWorkerAgentV2("", agents, "修复前端 React 组件里的 bug，并补充 Go API 测试", "1. Inspect frontend state\n2. Patch backend API\n3. Run tests")

	if err != nil || decision.Agent.ID != "agent_coding" {
		t.Fatalf("expected coding agent, got %s with output:\n%s err=%v", decision.Agent.ID, decision.Output(), err)
	}
	if !strings.Contains(decision.Output(), "Candidate scores") {
		t.Fatalf("expected transparent candidate scores, got:\n%s", decision.Output())
	}
}

func TestSelectWorkerAgentChoosesResearchForMarketTask(t *testing.T) {
	agents := testAgents()

	decision, err := selectWorkerAgentV2("", agents, "Compare competitors and verify recent pricing sources for this product launch", "1. Gather sources\n2. Compare market positioning")

	if err != nil || decision.Agent.ID != "agent_research" {
		t.Fatalf("expected research agent, got %s with output:\n%s err=%v", decision.Agent.ID, decision.Output(), err)
	}
}

func TestSelectWorkerAgentChoosesDataForBudgetTask(t *testing.T) {
	agents := testAgents()

	decision, err := selectWorkerAgentV2("", agents, "计算下个季度预算、成本和 capacity tradeoff", "1. Estimate cost\n2. Compare budget scenarios")

	if err != nil || decision.Agent.ID != "agent_data" {
		t.Fatalf("expected data agent, got %s with output:\n%s err=%v", decision.Agent.ID, decision.Output(), err)
	}
}

func TestSelectWorkerAgentV2RefusesZeroEvidence(t *testing.T) {
	agents := []domain.Agent{{ID: "agent_invoices", Name: "Invoice Clerk", Description: "Reconciles invoices."}}
	decision, err := selectWorkerAgentV2("", agents, "Write a sonnet about winter", "Use vivid imagery")
	if !errors.Is(err, ErrNoSuitableAgent) || decision.Agent.ID != "" || decision.Scores[0].Score != 0 {
		t.Fatalf("expected typed no-suitable decision, got decision=%#v err=%v", decision, err)
	}
}

func TestSelectWorkerAgentV2AppliesDeclarativeExclusions(t *testing.T) {
	agents := []domain.Agent{
		{ID: "medical", Name: "Market Researcher", RoutingHints: domain.AgentRoutingHints{Capabilities: []string{"market"}, Exclusions: []string{"medical diagnosis"}}},
		{ID: "analyst", Name: "General Analyst", RoutingHints: domain.AgentRoutingHints{Capabilities: []string{"analysis"}}},
	}
	decision, err := selectWorkerAgentV2("", agents, "Analyze the market for medical diagnosis tools", "Provide analysis")
	if err != nil || decision.Agent.ID != "analyst" {
		t.Fatalf("expected exclusion to demote medical agent, got decision=%#v err=%v", decision, err)
	}
}

func TestSelectWorkerAgentV2DoesNotSubstringMatchShortEnglishHints(t *testing.T) {
	agents := []domain.Agent{{
		ID: "coding", Name: "Coding Agent",
		RoutingHints: domain.AgentRoutingHints{Capabilities: []string{"go"}},
	}}
	decision, err := selectWorkerAgentV2("", agents, "Set quarterly goals", "Draft objectives")
	if !errors.Is(err, ErrNoSuitableAgent) || decision.Scores[0].Score != 0 {
		t.Fatalf("short hint must match a complete token: decision=%#v err=%v", decision, err)
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

func TestV1AgentSelectionPolicyPreservesCentralKeywordFallback(t *testing.T) {
	runtime := &Runtime{}
	agents := []domain.Agent{{ID: "research", Name: "Research worker", Description: "research"}, {ID: "coding", Name: "Coding worker", Description: "software"}}
	decision, err := runtime.routeWorkerAgent(context.Background(), "run_legacy", restoredRuntime{
		routerMode: RouterModeQuery, agentSelectionPolicyVersion: AgentSelectionPolicyVersionV1, catalog: tools.DefaultCatalog(),
	}, agents, "implement and test an API", "debug the code")
	if err != nil || decision.Agent.ID != "coding" || decision.PolicyRevision != AgentSelectionPolicyVersionV1 {
		t.Fatalf("v1 route changed: decision=%#v err=%v", decision, err)
	}
}

func TestCurrentAgentSelectionPolicyReturnsTypedNoSuitableOutcome(t *testing.T) {
	runtime := &Runtime{}
	decision, err := runtime.routeWorkerAgent(context.Background(), "run_v2", restoredRuntime{
		routerMode: RouterModeQuery, agentSelectionPolicyVersion: CurrentAgentSelectionPolicyVersion, catalog: tools.DefaultCatalog(),
	}, []domain.Agent{{ID: "invoices", Name: "Invoice Clerk", Description: "Reconciles invoices."}}, "write a sonnet", "use imagery")
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
	eligible, evidence := eligibleWorkerAgents(agents, tools.DefaultCatalog())
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
	if collaboration.Run.RuntimeSnapshot.AgentSelectionPolicyVersion != CurrentAgentSelectionPolicyVersion {
		t.Fatalf("agent selection policy was not frozen: %#v", collaboration.Run.RuntimeSnapshot)
	}

	autonomous, err := runtime.PrepareAutonomousRunWithContract(context.Background(), custom.ID, conversation.ID, nil)
	if err != nil {
		t.Fatalf("prepare autonomous: %v", err)
	}
	if autonomous.WorkerAgent.ID != custom.ID || autonomous.Run.AgentID != custom.ID {
		t.Fatalf("expected autonomous to use requested agent, got agent=%s run_agent=%s", autonomous.WorkerAgent.ID, autonomous.Run.AgentID)
	}
}

func TestMultiAgentWorkerUsesBoundedIsolatedChildRun(t *testing.T) {
	fixtureStore := fixturestore.New()

	conversation, err := fixtureStore.CreateConversation("delegated collaboration")
	if err != nil {
		t.Fatal(err)
	}
	runtime := NewRuntime(RuntimeOptions{
		Store: fixtureStore, ModelClient: newLocalFallbackOpenAIClientForTest(), RouterMode: RouterModeQuery,
		ChildRuns: ChildRunLimits{
			MaxConcurrent: 1, MaxPerParent: 1, Timeout: time.Minute, SummaryMaxChars: 80,
			RunBudget: domain.RuntimeRunBudget{MaxModelCalls: 2, MaxTotalTokens: 4000, MaxToolCalls: 1},
		},
	})
	prepared, err := runtime.PrepareCollaborationRunWithContract(context.Background(), "agent_planner", conversation.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Deployment changes after plan creation must not alter this parent's frozen child contract.
	runtime.childRunLimits.Timeout = 5 * time.Second
	runtime.childRunLimits.SummaryMaxChars = 500
	runtime.childRunLimits.RunBudget.MaxModelCalls = 99
	events, errs := runtime.RunCollaboration(context.Background(), prepared, "Implement and test a Go API change")
	for range events {
	}
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
	events, errs = runtime.ContinueCollaboration(context.Background(), prepared.Run.ID, "Inspect, implement, and test the change.")
	for range events {
	}
	if err := <-errs; err != nil {
		t.Fatal(err)
	}

	delegations, err := fixtureStore.ListRunDelegations(prepared.Run.ID)
	if err != nil || len(delegations) != 1 {
		t.Fatalf("delegations=%#v err=%v", delegations, err)
	}
	relation := delegations[0]
	if relation.Status != domain.DelegationCompleted || relation.OutputRef == "" || !relation.SummaryTruncated {
		t.Fatalf("unexpected completed delegation: %#v", relation)
	}
	if len([]rune(relation.Summary)) > 80 {
		t.Fatalf("summary is not bounded: %d", len([]rune(relation.Summary)))
	}
	child, ok, err := fixtureStore.GetRun(relation.ChildRunID)
	if err != nil || !ok {
		t.Fatalf("child run: ok=%v err=%v", ok, err)
	}
	if child.Status != domain.RunCompleted || child.RuntimeSnapshot == nil || child.RuntimeSnapshot.Delegation == nil || !child.RuntimeSnapshot.Delegation.IsolatedContext {
		t.Fatalf("child run boundary = %#v", child)
	}
	if child.RuntimeSnapshot.RunBudget.MaxModelCalls != 2 || child.RuntimeSnapshot.RunBudget.MaxTotalTokens != 4000 {
		t.Fatalf("child budget = %#v", child.RuntimeSnapshot.RunBudget)
	}
	if child.RuntimeSnapshot.Delegation.TimeoutMS != time.Minute.Milliseconds() || child.RuntimeSnapshot.Delegation.SummaryMaxChars != 80 {
		t.Fatalf("child did not use parent-frozen delegation policy: %#v", child.RuntimeSnapshot.Delegation)
	}
	for _, name := range child.RuntimeSnapshot.Agent.Tools {
		if name == taskstate.UpdateToolName {
			t.Fatal("child inherited parent task-state authority")
		}
	}
	parentEvents, err := fixtureStore.ListRunEvents(prepared.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[domain.RunEventType]bool{}
	for _, item := range parentEvents {
		seen[item.Type] = true
	}
	for _, eventType := range []domain.RunEventType{domain.EventAgentSelectionDecided, domain.EventDelegationCreated, domain.EventDelegationStarted, domain.EventDelegationCompleted} {
		if !seen[eventType] {
			t.Fatalf("missing parent event %s", eventType)
		}
	}
}

func TestContinueCollaborationRefusesIneligibleCandidatesWithoutChildRun(t *testing.T) {
	fixtureStore := fixturestore.New()
	conversation, err := fixtureStore.CreateConversation("no eligible worker")
	if err != nil {
		t.Fatal(err)
	}
	runtime := NewRuntime(RuntimeOptions{
		Store: fixtureStore, ModelClient: newLocalFallbackOpenAIClientForTest(), RouterMode: RouterModeQuery,
	})
	snapshot := testRuntimeSnapshot()
	snapshot.Mode = ChatModeMultiAgent
	snapshot.AutonomousLimits = nil
	snapshot.RouterMode = RouterModeQuery
	snapshot.AgentSelectionPolicyVersion = CurrentAgentSelectionPolicyVersion
	snapshot.CandidateAgents = []domain.RuntimeAgentSnapshot{{
		ID: "agent_unavailable", Name: "Unavailable worker", Tools: []string{"not_frozen"}, Executor: domain.DefaultAgentExecutor,
	}}
	snapshot.ChildRunPolicy = &domain.RuntimeChildRunPolicy{
		MaxDepth: 1, TimeoutMS: time.Minute.Milliseconds(), SummaryMaxChars: 100,
		AgentDefinitionSource: "runtime_snapshot.candidate_agents", RunBudget: domain.RuntimeRunBudget{},
	}
	run, err := fixtureStore.CreateRunWithContract("agent_planner", conversation.ID, snapshot, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixtureStore.CreateCollaborationStep(domain.CollaborationStep{
		RunID: run.ID, ConversationID: conversation.ID, Role: "planner",
		Status: domain.CollaborationStepCompleted, Input: "Use an unavailable capability", Output: "Execute the plan.",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixtureStore.UpdateRunStatus(run.ID, domain.RunWaitingForUser, ""); err != nil {
		t.Fatal(err)
	}
	events, errs := runtime.ContinueCollaboration(context.Background(), run.ID, "Execute the plan.")
	for range events {
	}
	if err := <-errs; !errors.Is(err, ErrNoEligibleAgent) {
		t.Fatalf("expected typed no-route failure, got %v", err)
	}
	delegations, err := fixtureStore.ListRunDelegations(run.ID)
	if err != nil || len(delegations) != 0 {
		t.Fatalf("ineligible route created child delegation: %#v err=%v", delegations, err)
	}
	runEvents, err := fixtureStore.ListRunEvents(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range runEvents {
		if item.Type == domain.EventAgentSelectionDecided {
			found = item.Payload["outcome"] == AgentSelectionOutcomeNoEligible && item.Payload["policy_revision"] == CurrentAgentSelectionPolicyVersion
		}
	}
	if !found {
		t.Fatalf("missing no-route decision evidence: %#v", runEvents)
	}
}

func TestCancelParentRunPropagatesToActiveChild(t *testing.T) {
	fixtureStore := fixturestore.New()

	conversation, err := fixtureStore.CreateConversation("cancel delegated collaboration")
	if err != nil {
		t.Fatal(err)
	}
	client := &blockingAgentClient{
		Client: newLocalFallbackOpenAIClientForTest(), started: make(chan struct{}, 1),
	}
	runtime := NewRuntime(RuntimeOptions{
		Store: fixtureStore, ModelClient: client, RouterMode: RouterModeQuery,
		ChildRuns: ChildRunLimits{MaxConcurrent: 1, MaxPerParent: 1, Timeout: time.Minute, SummaryMaxChars: 100,
			RunBudget: domain.RuntimeRunBudget{MaxModelCalls: 2, MaxTotalTokens: 4000}},
	})
	prepared, err := runtime.PrepareCollaborationRunWithContract(context.Background(), "agent_planner", conversation.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	events, errs := runtime.RunCollaboration(context.Background(), prepared, "Implement a Go API change")
	for range events {
	}
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
	events, errs = runtime.ContinueCollaboration(context.Background(), prepared.Run.ID, "Implement and test.")
	drained := make(chan struct{})
	go func() {
		for range events {
		}
		close(drained)
	}()
	select {
	case <-client.started:
	case <-time.After(3 * time.Second):
		t.Fatal("child worker did not start")
	}
	if canceled, err := runtime.CancelRun(prepared.Run.ID); err != nil || canceled.Status != domain.RunCanceling {
		t.Fatalf("cancel parent: run=%#v err=%v", canceled, err)
	}
	runErr := <-errs
	if !errors.Is(runErr, context.Canceled) {
		t.Fatalf("continuation error = %v", runErr)
	}
	<-drained
	if final, err := runtime.FailRun(prepared.Run.ID, runErr); err != nil || final.Status != domain.RunCanceled {
		t.Fatalf("finalize canceled parent: run=%#v err=%v", final, err)
	}
	items, err := fixtureStore.ListRunDelegations(prepared.Run.ID)
	if err != nil || len(items) != 1 || items[0].Status != domain.DelegationCanceled {
		t.Fatalf("delegation after cancel=%#v err=%v", items, err)
	}
	child, ok, err := fixtureStore.GetRun(items[0].ChildRunID)
	if err != nil || !ok || child.Status != domain.RunCanceled {
		t.Fatalf("child after cancel=%#v ok=%v err=%v", child, ok, err)
	}
}

func TestResumeRecoverableCollaborationReusesInterruptedChild(t *testing.T) {
	fixtureStore := fixturestore.New()

	conversation, err := fixtureStore.CreateConversation("resume delegated collaboration")
	if err != nil {
		t.Fatal(err)
	}
	runtime := NewRuntime(RuntimeOptions{
		Store: fixtureStore, ModelClient: newLocalFallbackOpenAIClientForTest(), RouterMode: RouterModeQuery,
		ChildRuns: ChildRunLimits{MaxConcurrent: 1, MaxPerParent: 1, Timeout: time.Minute, SummaryMaxChars: 120,
			RunBudget: domain.RuntimeRunBudget{MaxModelCalls: 2, MaxTotalTokens: 4000}},
	})
	prepared, err := runtime.PrepareCollaborationRunWithContract(context.Background(), "agent_planner", conversation.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	events, errs := runtime.RunCollaboration(context.Background(), prepared, "Implement a recoverable Go change")
	for range events {
	}
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
	parent, ok, err := fixtureStore.GetRun(prepared.Run.ID)
	if err != nil || !ok {
		t.Fatalf("parent ok=%v err=%v", ok, err)
	}
	routerStep, err := fixtureStore.CreateCollaborationStep(domain.CollaborationStep{
		RunID: parent.ID, ConversationID: conversation.ID, Role: "router", AgentID: "agent_planner",
		Status: domain.CollaborationStepCompleted, Input: "route", Output: "agent_planner",
	})
	if err != nil {
		t.Fatal(err)
	}
	workerStep, err := fixtureStore.CreateCollaborationStep(domain.CollaborationStep{
		RunID: parent.ID, ConversationID: conversation.ID, Role: "worker", AgentID: "agent_planner",
		Status: domain.CollaborationStepFailed, Input: "delegated work", Error: "worker interrupted",
	})
	if err != nil {
		t.Fatal(err)
	}
	selected, found := findAgentByID(restoreCandidates(parent.RuntimeSnapshot.CandidateAgents), routerStep.AgentID)
	if !found {
		t.Fatal("frozen worker candidate not found")
	}
	childSnapshot, err := runtime.childRuntimeSnapshot(parent, selected, "delegation-resume", "turn-resume", workerStep.ID)
	if err != nil {
		t.Fatal(err)
	}
	child, relation, err := fixtureStore.CreateChildRun(domain.ChildRunRequest{
		Delegation: domain.RunDelegation{
			ID: "delegation-resume", ParentRunID: parent.ID, ParentTurnID: "turn-resume",
			ParentStageID: workerStep.ID, AgentID: selected.ID, Depth: 1, Task: workerStep.Input, TimeoutMS: time.Minute.Milliseconds(),
		}, RuntimeSnapshot: childSnapshot,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixtureStore.UpdateRunStatus(child.ID, domain.RunFailedRecoverable, "worker interrupted"); err != nil {
		t.Fatal(err)
	}
	if _, err := fixtureStore.UpdateRunDelegation(relation.ID, domain.DelegationResult{Status: domain.DelegationBlocked, BlockReason: domain.DelegationBlockReasonChildRecoveryRequired, Error: "worker interrupted"}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixtureStore.UpdateRunStatus(parent.ID, domain.RunFailedRecoverable, "worker interrupted"); err != nil {
		t.Fatal(err)
	}

	events, errs = runtime.ResumeRecoverableCollaboration(context.Background(), parent.ID)
	for range events {
	}
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
	updated, ok, err := fixtureStore.GetRunDelegation(relation.ID)
	if err != nil || !ok || updated.Status != domain.DelegationCompleted || updated.ChildRunID != child.ID {
		t.Fatalf("resumed relation=%#v ok=%v err=%v", updated, ok, err)
	}
	updatedChild, ok, err := fixtureStore.GetRun(child.ID)
	if err != nil || !ok || updatedChild.Status != domain.RunCompleted {
		t.Fatalf("resumed child=%#v ok=%v err=%v", updatedChild, ok, err)
	}
	steps, err := fixtureStore.ListCollaborationSteps(parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := findCollaborationStep(steps, "reviewer"); !ok {
		t.Fatal("reviewer did not run after child resume")
	}
	if _, ok := findCollaborationStep(steps, "finalizer"); !ok {
		t.Fatal("finalizer did not run after child resume")
	}
}

func TestFailedChildDoesNotEnterParentReviewContext(t *testing.T) {
	fixtureStore := fixturestore.New()

	conversation, err := fixtureStore.CreateConversation("failed delegated collaboration")
	if err != nil {
		t.Fatal(err)
	}
	client := &failingAgentClient{Client: newLocalFallbackOpenAIClientForTest()}
	runtime := NewRuntime(RuntimeOptions{
		Store: fixtureStore, ModelClient: client, RouterMode: RouterModeQuery,
		ChildRuns: ChildRunLimits{MaxConcurrent: 1, MaxPerParent: 1, Timeout: time.Minute,
			RunBudget: domain.RuntimeRunBudget{MaxModelCalls: 2, MaxTotalTokens: 4000}},
	})
	prepared, err := runtime.PrepareCollaborationRunWithContract(context.Background(), "agent_planner", conversation.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	events, errs := runtime.RunCollaboration(context.Background(), prepared, "Implement a failing delegated task")
	for range events {
	}
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
	events, errs = runtime.ContinueCollaboration(context.Background(), prepared.Run.ID, "Execute once.")
	for range events {
	}
	if err := <-errs; err == nil || !strings.Contains(err.Error(), "forced child failure") {
		t.Fatalf("continuation error = %v", err)
	}
	steps, err := fixtureStore.ListCollaborationSteps(prepared.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, found := findCollaborationStep(steps, "reviewer"); found {
		t.Fatal("reviewer received failed child output")
	}
	if _, found := findCollaborationStep(steps, "finalizer"); found {
		t.Fatal("finalizer ran after failed child")
	}
	worker, found := findCollaborationStep(steps, "worker")
	if !found || worker.Output != "" || worker.Status != domain.CollaborationStepFailed {
		t.Fatalf("parent worker stage = %#v", worker)
	}
	items, err := fixtureStore.ListRunDelegations(prepared.Run.ID)
	if err != nil || len(items) != 1 || items[0].Status != domain.DelegationFailed || items[0].Summary != "" {
		t.Fatalf("failed delegation=%#v err=%v", items, err)
	}
}

func TestChildBackpressureLeavesParentWaitingForRetry(t *testing.T) {
	fixtureStore := fixturestore.New()

	conversation, err := fixtureStore.CreateConversation("delegation backpressure")
	if err != nil {
		t.Fatal(err)
	}
	runtime := NewRuntime(RuntimeOptions{
		Store: fixtureStore, ModelClient: newLocalFallbackOpenAIClientForTest(), RouterMode: RouterModeQuery,
		ChildRuns: ChildRunLimits{MaxConcurrent: 1, MaxPerParent: 1, Timeout: time.Minute,
			RunBudget: domain.RuntimeRunBudget{MaxModelCalls: 2}},
	})
	prepared, err := runtime.PrepareCollaborationRunWithContract(context.Background(), "agent_planner", conversation.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	events, errs := runtime.RunCollaboration(context.Background(), prepared, "Wait for child capacity")
	for range events {
	}
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
	blocker, err := runtime.delegations.Reserve("other-parent", 1)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Release()
	events, errs = runtime.ContinueCollaboration(context.Background(), prepared.Run.ID, "Approved plan")
	for range events {
	}
	runErr := <-errs
	info := failure.Describe(runErr)
	if info.Code != "child_run_capacity_exhausted" || !info.Retryable {
		t.Fatalf("backpressure = %#v err=%v", info, runErr)
	}
	parent, ok, err := fixtureStore.GetRun(prepared.Run.ID)
	if err != nil || !ok || parent.Status != domain.RunWaitingForUser {
		t.Fatalf("parent after backpressure=%#v ok=%v err=%v", parent, ok, err)
	}
}

func TestResumeRecoverableCollaborationValidatesRecoveryBoundary(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*testing.T, *Runtime, *fixturestore.Store, domain.Run)
		want  string
	}{
		{name: "planner missing", want: "planner step not found"},
		{name: "router missing", setup: func(t *testing.T, _ *Runtime, fixtureStore *fixturestore.Store, run domain.Run) {
			createRecoveryStep(t, fixtureStore, run, "planner", "agent_planner", "task", "plan")
		}, want: "router step not found"},
		{name: "frozen worker missing", setup: func(t *testing.T, _ *Runtime, fixtureStore *fixturestore.Store, run domain.Run) {
			createRecoveryStep(t, fixtureStore, run, "planner", "agent_planner", "task", "plan")
			createRecoveryStep(t, fixtureStore, run, "router", "missing-agent", "route", "missing-agent")
		}, want: "frozen routed worker not found"},
		{name: "delegation missing", setup: func(t *testing.T, _ *Runtime, fixtureStore *fixturestore.Store, run domain.Run) {
			createRecoveryStep(t, fixtureStore, run, "planner", "agent_planner", "task", "plan")
			createRecoveryStep(t, fixtureStore, run, "router", "agent_planner", "route", "agent_planner")
		}, want: "exactly one delegation"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runtime, fixtureStore, run := newRecoverableCollaborationForTest(t)
			if test.setup != nil {
				test.setup(t, runtime, fixtureStore, run)
			}
			if err := resumeCollaborationError(runtime, run.ID); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected %q, got %v", test.want, err)
			}
		})
	}
}

func TestResumeRecoverableCollaborationRejectsRunPreconditions(t *testing.T) {
	runtime, fixtureStore, run := newRecoverableCollaborationForTest(t)
	if err := resumeCollaborationError(runtime, "missing"); err == nil || !store.IsNotFound(err) {
		t.Fatalf("expected missing run error, got %v", err)
	}
	if _, err := fixtureStore.UpdateRunStatus(run.ID, domain.RunRunning, ""); err != nil {
		t.Fatal(err)
	}
	if err := resumeCollaborationError(runtime, run.ID); err == nil || !strings.Contains(err.Error(), "not recoverable") {
		t.Fatalf("expected non-recoverable error, got %v", err)
	}

	conversation, err := fixtureStore.CreateConversation("wrong recovery mode")
	if err != nil {
		t.Fatal(err)
	}
	snapshot := testRuntimeSnapshot()
	snapshot.Mode = ChatModeSingle
	snapshot.AutonomousLimits = nil
	single, err := fixtureStore.CreateRunWithContract("agent_planner", conversation.ID, snapshot, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixtureStore.UpdateRunStatus(single.ID, domain.RunFailedRecoverable, "interrupted"); err != nil {
		t.Fatal(err)
	}
	if err := resumeCollaborationError(runtime, single.ID); err == nil || !strings.Contains(err.Error(), "not \"multi_agent\"") {
		t.Fatalf("expected wrong mode error, got %v", err)
	}
}

func TestResumeRecoverableCollaborationUsesCompletedChildResult(t *testing.T) {
	runtime, fixtureStore, run := newRecoverableCollaborationForTest(t)
	planner := createRecoveryStep(t, fixtureStore, run, "planner", "agent_planner", "task", "approved plan")
	_ = planner
	createRecoveryStep(t, fixtureStore, run, "router", "agent_planner", "route", "agent_planner")
	worker := createRecoveryStep(t, fixtureStore, run, "worker", "agent_planner", "delegated task", "")
	worker, err := fixtureStore.UpdateCollaborationStep(worker.ID, domain.CollaborationStepFailed, "", "worker interrupted")
	if err != nil {
		t.Fatal(err)
	}
	selected, found := findAgentByID(restoreCandidates(run.RuntimeSnapshot.CandidateAgents), "agent_planner")
	if !found {
		t.Fatal("frozen agent not found")
	}
	childSnapshot, err := runtime.childRuntimeSnapshot(run, selected, "delegation-completed", "turn-completed", worker.ID)
	if err != nil {
		t.Fatal(err)
	}
	child, relation, err := fixtureStore.CreateChildRun(domain.ChildRunRequest{
		Delegation: domain.RunDelegation{
			ID: "delegation-completed", ParentRunID: run.ID, ParentTurnID: "turn-completed",
			ParentStageID: worker.ID, AgentID: selected.ID, Depth: 1,
			Task: worker.Input, TimeoutMS: childSnapshot.Delegation.TimeoutMS,
		},
		RuntimeSnapshot: childSnapshot,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixtureStore.UpdateRunDelegation(relation.ID, domain.DelegationResult{
		Status: domain.DelegationCompleted, Summary: "durable child summary",
		OutputRef: "run://" + child.ID + "/stages/worker", OutputHash: "hash", OutputBytes: 21,
	}); err != nil {
		t.Fatal(err)
	}
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
	if _, ok := findCollaborationStep(steps, "reviewer"); !ok {
		t.Fatal("reviewer did not consume durable child result")
	}
	if _, ok := findCollaborationStep(steps, "finalizer"); !ok {
		t.Fatal("finalizer did not complete after durable child result")
	}
}

func TestResumeRecoverableCollaborationRejectsNonResumableDelegation(t *testing.T) {
	runtime, fixtureStore, run := newRecoverableCollaborationForTest(t)
	createRecoveryStep(t, fixtureStore, run, "planner", "agent_planner", "task", "plan")
	createRecoveryStep(t, fixtureStore, run, "router", "agent_planner", "route", "agent_planner")
	worker := createRecoveryStep(t, fixtureStore, run, "worker", "agent_planner", "work", "")
	selected, _ := findAgentByID(restoreCandidates(run.RuntimeSnapshot.CandidateAgents), "agent_planner")
	childSnapshot, err := runtime.childRuntimeSnapshot(run, selected, "delegation-created", "turn-created", worker.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, relation, err := fixtureStore.CreateChildRun(domain.ChildRunRequest{
		Delegation: domain.RunDelegation{
			ID: "delegation-created", ParentRunID: run.ID, ParentTurnID: "turn-created",
			ParentStageID: worker.ID, AgentID: selected.ID, Depth: 1,
			Task: worker.Input, TimeoutMS: childSnapshot.Delegation.TimeoutMS,
		},
		RuntimeSnapshot: childSnapshot,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := resumeCollaborationError(runtime, run.ID); err == nil || !strings.Contains(err.Error(), "not resumable") {
		t.Fatalf("expected created delegation rejection, got %v", err)
	}
	if _, err := fixtureStore.UpdateRunDelegation(relation.ID, domain.DelegationResult{Status: domain.DelegationCompleted}); err != nil {
		t.Fatal(err)
	}
	if err := resumeCollaborationError(runtime, run.ID); err == nil || !strings.Contains(err.Error(), "no child output reference") {
		t.Fatalf("expected missing output reference error, got %v", err)
	}
}

func TestResumeRecoverableCollaborationValidatesBlockedChildState(t *testing.T) {
	t.Run("parent stage missing", func(t *testing.T) {
		runtime, fixtureStore, run, _, relation := recoverableDelegationFixture(t)
		fault := runtimeStoreFault{Store: fixtureStore, runDelegations: []domain.RunDelegation{relation}}
		fault.runDelegations[0].ParentStageID = "missing-stage"
		runtime = NewRuntime(RuntimeOptions{Store: &fault, ModelClient: newLocalFallbackOpenAIClientForTest(), ChildRuns: runtime.childRunLimits})
		if err := resumeCollaborationError(runtime, run.ID); err == nil || !strings.Contains(err.Error(), "parent worker delegation stage not found") {
			t.Fatalf("expected missing parent stage error, got %v", err)
		}
	})

	t.Run("unsupported block reason", func(t *testing.T) {
		runtime, fixtureStore, run, _, relation := recoverableDelegationFixture(t)
		fault := runtimeStoreFault{Store: fixtureStore, runDelegations: []domain.RunDelegation{relation}}
		fault.runDelegations[0].Status = domain.DelegationBlocked
		fault.runDelegations[0].BlockReason = "manual_review"
		runtime = NewRuntime(RuntimeOptions{Store: &fault, ModelClient: newLocalFallbackOpenAIClientForTest(), ChildRuns: runtime.childRunLimits})
		if err := resumeCollaborationError(runtime, run.ID); err == nil || !strings.Contains(err.Error(), "unsupported block reason") {
			t.Fatalf("expected unsupported block reason error, got %v", err)
		}
	})

	t.Run("child is not recoverable", func(t *testing.T) {
		runtime, fixtureStore, run, _, relation := recoverableDelegationFixture(t)
		if _, err := fixtureStore.UpdateRunDelegation(relation.ID, domain.DelegationResult{Status: domain.DelegationBlocked, BlockReason: domain.DelegationBlockReasonChildRecoveryRequired}); err != nil {
			t.Fatal(err)
		}
		if err := resumeCollaborationError(runtime, run.ID); err == nil || !strings.Contains(err.Error(), "child run is not recoverable") {
			t.Fatalf("expected child state error, got %v", err)
		}
	})

	t.Run("child capacity exhausted", func(t *testing.T) {
		runtime, fixtureStore, run, child, relation := recoverableDelegationFixture(t)
		if _, err := fixtureStore.UpdateRunStatus(child.ID, domain.RunFailedRecoverable, "interrupted"); err != nil {
			t.Fatal(err)
		}
		if _, err := fixtureStore.UpdateRunDelegation(relation.ID, domain.DelegationResult{Status: domain.DelegationBlocked, BlockReason: domain.DelegationBlockReasonChildRecoveryRequired}); err != nil {
			t.Fatal(err)
		}
		blocker, err := runtime.delegations.Reserve("other-parent", 1)
		if err != nil {
			t.Fatal(err)
		}
		defer blocker.Release()
		if err := resumeCollaborationError(runtime, run.ID); err == nil || failure.Describe(err).Category != failure.CategoryCapacity {
			t.Fatalf("expected child capacity error, got %v", err)
		}
	})
}

func TestResumeRecoverableCollaborationPropagatesStoreFailures(t *testing.T) {
	tests := []struct {
		name  string
		fault func(*runtimeStoreFault, domain.Run, domain.Run, domain.RunDelegation)
		want  string
	}{
		{name: "list delegations", fault: func(fault *runtimeStoreFault, _, _ domain.Run, _ domain.RunDelegation) {
			fault.failListDelegations = true
		}},
		{name: "get child", fault: func(fault *runtimeStoreFault, _ domain.Run, child domain.Run, relation domain.RunDelegation) {
			fault.failGetRunID = child.ID
			relation.Status = domain.DelegationBlocked
			relation.BlockReason = domain.DelegationBlockReasonChildRecoveryRequired
			fault.runDelegations = []domain.RunDelegation{relation}
		}, want: "delegation store test failure"},
		{name: "update parent run", fault: func(fault *runtimeStoreFault, _ domain.Run, _ domain.Run, relation domain.RunDelegation) {
			fault.failUpdateRunStatus = domain.RunRunning
			fault.runDelegations = []domain.RunDelegation{completedDelegationForResume(relation)}
		}},
		{name: "update parent worker", fault: func(fault *runtimeStoreFault, _ domain.Run, _ domain.Run, _ domain.RunDelegation) {
			fault.failUpdateStepStatus = domain.CollaborationStepRunning
		}},
		{name: "publish parent worker", fault: func(fault *runtimeStoreFault, _ domain.Run, _ domain.Run, _ domain.RunDelegation) {
			fault.failEventType = domain.EventStageStarted
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			baseRuntime, fixtureStore, run, child, relation := recoverableDelegationFixture(t)
			if test.name == "update parent run" {
				if _, err := fixtureStore.UpdateRunDelegation(relation.ID, domain.DelegationResult{Status: domain.DelegationCompleted, OutputRef: "run://" + child.ID}); err != nil {
					t.Fatal(err)
				}
			} else if test.name != "list delegations" {
				if _, err := fixtureStore.UpdateRunStatus(child.ID, domain.RunFailedRecoverable, "interrupted"); err != nil {
					t.Fatal(err)
				}
				if _, err := fixtureStore.UpdateRunDelegation(relation.ID, domain.DelegationResult{Status: domain.DelegationBlocked, BlockReason: domain.DelegationBlockReasonChildRecoveryRequired}); err != nil {
					t.Fatal(err)
				}
			}
			fault := runtimeStoreFault{Store: fixtureStore}
			test.fault(&fault, run, child, relation)
			runtime := NewRuntime(RuntimeOptions{Store: &fault, ModelClient: newLocalFallbackOpenAIClientForTest(), ChildRuns: baseRuntime.childRunLimits})
			if err := resumeCollaborationError(runtime, run.ID); err == nil || test.want != "" && !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected store failure, got %v", err)
			}
		})
	}
}

func recoverableDelegationFixture(t *testing.T) (*Runtime, *fixturestore.Store, domain.Run, domain.Run, domain.RunDelegation) {
	t.Helper()
	runtime, fixtureStore, run := newRecoverableCollaborationForTest(t)
	createRecoveryStep(t, fixtureStore, run, "planner", "agent_planner", "task", "plan")
	createRecoveryStep(t, fixtureStore, run, "router", "agent_planner", "route", "agent_planner")
	worker := createRecoveryStep(t, fixtureStore, run, "worker", "agent_planner", "work", "")
	if _, err := fixtureStore.UpdateCollaborationStep(worker.ID, domain.CollaborationStepFailed, "", "interrupted"); err != nil {
		t.Fatal(err)
	}
	selected, found := findAgentByID(restoreCandidates(run.RuntimeSnapshot.CandidateAgents), "agent_planner")
	if !found {
		t.Fatal("frozen agent not found")
	}
	childSnapshot, err := runtime.childRuntimeSnapshot(run, selected, "delegation-boundary", "turn-boundary", worker.ID)
	if err != nil {
		t.Fatal(err)
	}
	child, relation, err := fixtureStore.CreateChildRun(domain.ChildRunRequest{
		Delegation: domain.RunDelegation{
			ID: "delegation-boundary", ParentRunID: run.ID, ParentTurnID: "turn-boundary",
			ParentStageID: worker.ID, AgentID: selected.ID, Depth: 1,
			Task: worker.Input, TimeoutMS: childSnapshot.Delegation.TimeoutMS,
		},
		RuntimeSnapshot: childSnapshot,
	})
	if err != nil {
		t.Fatal(err)
	}
	return runtime, fixtureStore, run, child, relation
}

func completedDelegationForResume(relation domain.RunDelegation) domain.RunDelegation {
	relation.Status = domain.DelegationCompleted
	relation.OutputRef = "run://" + relation.ChildRunID
	return relation
}

func newRecoverableCollaborationForTest(t *testing.T) (*Runtime, *fixturestore.Store, domain.Run) {
	t.Helper()
	fixtureStore := fixturestore.New()

	conversation, err := fixtureStore.CreateConversation("recoverable collaboration boundary")
	if err != nil {
		t.Fatal(err)
	}
	runtime := NewRuntime(RuntimeOptions{
		Store: fixtureStore, ModelClient: newLocalFallbackOpenAIClientForTest(), RouterMode: RouterModeQuery,
		ChildRuns: ChildRunLimits{MaxConcurrent: 1, MaxPerParent: 1, Timeout: time.Minute, SummaryMaxChars: 100,
			RunBudget: domain.RuntimeRunBudget{MaxModelCalls: 2, MaxTotalTokens: 4000}},
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

func createRecoveryStep(t *testing.T, fixtureStore *fixturestore.Store, run domain.Run, role, agentID, input, output string) domain.CollaborationStep {
	t.Helper()
	step, err := fixtureStore.CreateCollaborationStep(domain.CollaborationStep{
		RunID: run.ID, ConversationID: run.ConversationID, Role: role, AgentID: agentID,
		Status: domain.CollaborationStepCompleted, Input: input, Output: output,
	})
	if err != nil {
		t.Fatal(err)
	}
	return step
}

func resumeCollaborationError(runtime *Runtime, runID string) error {
	events, errs := runtime.ResumeRecoverableCollaboration(context.Background(), runID)
	for range events {
	}
	return <-errs
}

func restoreCandidates(items []domain.RuntimeAgentSnapshot) []domain.Agent {
	result := make([]domain.Agent, 0, len(items))
	for _, item := range items {
		result = append(result, restoreAgent(item))
	}
	return result
}

type blockingAgentClient struct {
	modelprovider.Client
	started chan struct{}
}

type failingAgentClient struct{ modelprovider.Client }

func (c *failingAgentClient) WithRuntimeIdentity(identity modelprovider.RuntimeIdentity) modelprovider.Client {
	return &failingAgentClient{Client: c.Client.WithRuntimeIdentity(identity)}
}

func (c *failingAgentClient) StreamAgentChatWithToolsTrace(context.Context, string, []domain.Message, string, *tools.Catalog, *eventpkg.Recorder, string, string, []domain.RetrievedMemory, []domain.RetrievedDocumentChunk) (<-chan modelprovider.StreamEvent, <-chan error) {
	events := make(chan modelprovider.StreamEvent)
	errs := make(chan error, 1)
	close(events)
	errs <- errors.New("forced child failure")
	close(errs)
	return events, errs
}

func (c *blockingAgentClient) WithRuntimeIdentity(identity modelprovider.RuntimeIdentity) modelprovider.Client {
	return &blockingAgentClient{Client: c.Client.WithRuntimeIdentity(identity), started: c.started}
}

func (c *blockingAgentClient) StreamAgentChatWithToolsTrace(ctx context.Context, _ string, _ []domain.Message, _ string, _ *tools.Catalog, _ *eventpkg.Recorder, _, _ string, _ []domain.RetrievedMemory, _ []domain.RetrievedDocumentChunk) (<-chan modelprovider.StreamEvent, <-chan error) {
	events := make(chan modelprovider.StreamEvent)
	errs := make(chan error, 1)
	select {
	case c.started <- struct{}{}:
	default:
	}
	go func() {
		defer close(events)
		defer close(errs)
		<-ctx.Done()
		errs <- ctx.Err()
	}()
	return events, errs
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
	runtime := NewRuntime(RuntimeOptions{
		Store: fixtureStore, ModelClient: newLocalFallbackOpenAIClientForTest(), RouterMode: RouterModeQuery,
		Autonomous: AutonomousLimits{
			MaxIterations: 1, MaxRuntime: time.Minute, MaxOutputChars: 60000, MaxToolCalls: 20,
		},
	})
	prepared, err := runtime.PrepareAutonomousRunWithContract(context.Background(), "", conversation.ID, nil)
	if err != nil {
		t.Fatalf("prepare autonomous run: %v", err)
	}

	events, errs := runtime.RunAutonomous(context.Background(), prepared, "Write a concise project update.")
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
