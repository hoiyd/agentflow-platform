package agent

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

func TestPreparedRunsUseRequestedAgent(t *testing.T) {
	fileStore, err := store.NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	runtime := NewRuntime(RuntimeOptions{Store: fileStore, ModelClient: newLocalFallbackOpenAIClientForTest()})
	custom, err := fileStore.CreateAgent(domain.Agent{
		Name:             "Resume Reviewer",
		Description:      "Reviews resumes against job descriptions.",
		SystemPrompt:     "Review resume evidence.",
		MemoryEnabled:    true,
		RetrievalEnabled: true,
		Executor:         ExecutorNative,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	conversation, err := fileStore.CreateConversation("Custom agent run")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}

	collaboration, err := runtime.PrepareCollaborationRun(context.Background(), custom.ID, conversation.ID)
	if err != nil {
		t.Fatalf("prepare collaboration: %v", err)
	}
	if collaboration.WorkerAgent.ID != custom.ID || collaboration.Run.AgentID != custom.ID {
		t.Fatalf("expected collaboration to use requested agent, got agent=%s run_agent=%s", collaboration.WorkerAgent.ID, collaboration.Run.AgentID)
	}

	autonomous, err := runtime.PrepareAutonomousRun(context.Background(), custom.ID, conversation.ID)
	if err != nil {
		t.Fatalf("prepare autonomous: %v", err)
	}
	if autonomous.WorkerAgent.ID != custom.ID || autonomous.Run.AgentID != custom.ID {
		t.Fatalf("expected autonomous to use requested agent, got agent=%s run_agent=%s", autonomous.WorkerAgent.ID, autonomous.Run.AgentID)
	}
}

func TestMultiAgentWorkerUsesBoundedIsolatedChildRun(t *testing.T) {
	fileStore, err := store.NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := fileStore.CreateConversation("delegated collaboration")
	if err != nil {
		t.Fatal(err)
	}
	runtime := NewRuntime(RuntimeOptions{
		Store: fileStore, ModelClient: newLocalFallbackOpenAIClientForTest(), RouterMode: RouterModeQuery,
		ChildRuns: ChildRunLimits{
			MaxConcurrent: 1, MaxPerParent: 1, Timeout: time.Minute, SummaryMaxChars: 80,
			RunBudget: domain.RuntimeRunBudget{MaxModelCalls: 2, MaxTotalTokens: 4000, MaxToolCalls: 1},
		},
	})
	prepared, err := runtime.PrepareCollaborationRun(context.Background(), "agent_planner", conversation.ID)
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

	delegations, err := fileStore.ListRunDelegations(prepared.Run.ID)
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
	child, ok, err := fileStore.GetRun(relation.ChildRunID)
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
	parentEvents, err := fileStore.ListRunEvents(prepared.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[domain.RunEventType]bool{}
	for _, item := range parentEvents {
		seen[item.Type] = true
	}
	for _, eventType := range []domain.RunEventType{domain.EventDelegationCreated, domain.EventDelegationStarted, domain.EventDelegationCompleted} {
		if !seen[eventType] {
			t.Fatalf("missing parent event %s", eventType)
		}
	}
}

func TestCancelParentRunPropagatesToActiveChild(t *testing.T) {
	fileStore, err := store.NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := fileStore.CreateConversation("cancel delegated collaboration")
	if err != nil {
		t.Fatal(err)
	}
	client := &blockingAgentClient{
		Client: newLocalFallbackOpenAIClientForTest(), started: make(chan struct{}, 1),
	}
	runtime := NewRuntime(RuntimeOptions{
		Store: fileStore, ModelClient: client, RouterMode: RouterModeQuery,
		ChildRuns: ChildRunLimits{MaxConcurrent: 1, MaxPerParent: 1, Timeout: time.Minute, SummaryMaxChars: 100,
			RunBudget: domain.RuntimeRunBudget{MaxModelCalls: 2, MaxTotalTokens: 4000}},
	})
	prepared, err := runtime.PrepareCollaborationRun(context.Background(), "agent_planner", conversation.ID)
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
	items, err := fileStore.ListRunDelegations(prepared.Run.ID)
	if err != nil || len(items) != 1 || items[0].Status != domain.DelegationCanceled {
		t.Fatalf("delegation after cancel=%#v err=%v", items, err)
	}
	child, ok, err := fileStore.GetRun(items[0].ChildRunID)
	if err != nil || !ok || child.Status != domain.RunCanceled {
		t.Fatalf("child after cancel=%#v ok=%v err=%v", child, ok, err)
	}
}

func TestResumeRecoverableCollaborationReusesInterruptedChild(t *testing.T) {
	fileStore, err := store.NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := fileStore.CreateConversation("resume delegated collaboration")
	if err != nil {
		t.Fatal(err)
	}
	runtime := NewRuntime(RuntimeOptions{
		Store: fileStore, ModelClient: newLocalFallbackOpenAIClientForTest(), RouterMode: RouterModeQuery,
		ChildRuns: ChildRunLimits{MaxConcurrent: 1, MaxPerParent: 1, Timeout: time.Minute, SummaryMaxChars: 120,
			RunBudget: domain.RuntimeRunBudget{MaxModelCalls: 2, MaxTotalTokens: 4000}},
	})
	prepared, err := runtime.PrepareCollaborationRun(context.Background(), "agent_planner", conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	events, errs := runtime.RunCollaboration(context.Background(), prepared, "Implement a recoverable Go change")
	for range events {
	}
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
	parent, ok, err := fileStore.GetRun(prepared.Run.ID)
	if err != nil || !ok {
		t.Fatalf("parent ok=%v err=%v", ok, err)
	}
	routerStep, err := fileStore.CreateCollaborationStep(domain.CollaborationStep{
		RunID: parent.ID, ConversationID: conversation.ID, Role: "router", AgentID: "agent_planner",
		Status: domain.CollaborationStepCompleted, Input: "route", Output: "agent_planner",
	})
	if err != nil {
		t.Fatal(err)
	}
	workerStep, err := fileStore.CreateCollaborationStep(domain.CollaborationStep{
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
	child, relation, err := fileStore.CreateChildRun(domain.ChildRunRequest{
		Delegation: domain.RunDelegation{
			ID: "delegation-resume", ParentRunID: parent.ID, ParentTurnID: "turn-resume",
			ParentStageID: workerStep.ID, AgentID: selected.ID, Depth: 1, Task: workerStep.Input, TimeoutMS: time.Minute.Milliseconds(),
		}, RuntimeSnapshot: childSnapshot,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fileStore.UpdateRunStatus(child.ID, domain.RunFailedRecoverable, "worker interrupted"); err != nil {
		t.Fatal(err)
	}
	if _, err := fileStore.UpdateRunDelegation(relation.ID, domain.DelegationResult{Status: domain.DelegationBlocked, BlockReason: domain.DelegationBlockReasonChildRecoveryRequired, Error: "worker interrupted"}); err != nil {
		t.Fatal(err)
	}
	if _, err := fileStore.UpdateRunStatus(parent.ID, domain.RunFailedRecoverable, "worker interrupted"); err != nil {
		t.Fatal(err)
	}

	events, errs = runtime.ResumeRecoverableCollaboration(context.Background(), parent.ID)
	for range events {
	}
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
	updated, ok, err := fileStore.GetRunDelegation(relation.ID)
	if err != nil || !ok || updated.Status != domain.DelegationCompleted || updated.ChildRunID != child.ID {
		t.Fatalf("resumed relation=%#v ok=%v err=%v", updated, ok, err)
	}
	updatedChild, ok, err := fileStore.GetRun(child.ID)
	if err != nil || !ok || updatedChild.Status != domain.RunCompleted {
		t.Fatalf("resumed child=%#v ok=%v err=%v", updatedChild, ok, err)
	}
	steps, err := fileStore.ListCollaborationSteps(parent.ID)
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
	fileStore, err := store.NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := fileStore.CreateConversation("failed delegated collaboration")
	if err != nil {
		t.Fatal(err)
	}
	client := &failingAgentClient{Client: newLocalFallbackOpenAIClientForTest()}
	runtime := NewRuntime(RuntimeOptions{
		Store: fileStore, ModelClient: client, RouterMode: RouterModeQuery,
		ChildRuns: ChildRunLimits{MaxConcurrent: 1, MaxPerParent: 1, Timeout: time.Minute,
			RunBudget: domain.RuntimeRunBudget{MaxModelCalls: 2, MaxTotalTokens: 4000}},
	})
	prepared, err := runtime.PrepareCollaborationRun(context.Background(), "agent_planner", conversation.ID)
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
	steps, err := fileStore.ListCollaborationSteps(prepared.Run.ID)
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
	items, err := fileStore.ListRunDelegations(prepared.Run.ID)
	if err != nil || len(items) != 1 || items[0].Status != domain.DelegationFailed || items[0].Summary != "" {
		t.Fatalf("failed delegation=%#v err=%v", items, err)
	}
}

func TestChildBackpressureLeavesParentWaitingForRetry(t *testing.T) {
	fileStore, err := store.NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := fileStore.CreateConversation("delegation backpressure")
	if err != nil {
		t.Fatal(err)
	}
	runtime := NewRuntime(RuntimeOptions{
		Store: fileStore, ModelClient: newLocalFallbackOpenAIClientForTest(), RouterMode: RouterModeQuery,
		ChildRuns: ChildRunLimits{MaxConcurrent: 1, MaxPerParent: 1, Timeout: time.Minute,
			RunBudget: domain.RuntimeRunBudget{MaxModelCalls: 2}},
	})
	prepared, err := runtime.PrepareCollaborationRun(context.Background(), "agent_planner", conversation.ID)
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
	parent, ok, err := fileStore.GetRun(prepared.Run.ID)
	if err != nil || !ok || parent.Status != domain.RunWaitingForUser {
		t.Fatalf("parent after backpressure=%#v ok=%v err=%v", parent, ok, err)
	}
}

func TestResumeRecoverableCollaborationValidatesRecoveryBoundary(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*testing.T, *Runtime, *store.FileStore, domain.Run)
		want  string
	}{
		{name: "planner missing", want: "planner step not found"},
		{name: "router missing", setup: func(t *testing.T, _ *Runtime, fileStore *store.FileStore, run domain.Run) {
			createRecoveryStep(t, fileStore, run, "planner", "agent_planner", "task", "plan")
		}, want: "router step not found"},
		{name: "frozen worker missing", setup: func(t *testing.T, _ *Runtime, fileStore *store.FileStore, run domain.Run) {
			createRecoveryStep(t, fileStore, run, "planner", "agent_planner", "task", "plan")
			createRecoveryStep(t, fileStore, run, "router", "missing-agent", "route", "missing-agent")
		}, want: "frozen routed worker not found"},
		{name: "delegation missing", setup: func(t *testing.T, _ *Runtime, fileStore *store.FileStore, run domain.Run) {
			createRecoveryStep(t, fileStore, run, "planner", "agent_planner", "task", "plan")
			createRecoveryStep(t, fileStore, run, "router", "agent_planner", "route", "agent_planner")
		}, want: "exactly one delegation"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runtime, fileStore, run := newRecoverableCollaborationForTest(t)
			if test.setup != nil {
				test.setup(t, runtime, fileStore, run)
			}
			if err := resumeCollaborationError(runtime, run.ID); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected %q, got %v", test.want, err)
			}
		})
	}
}

func TestResumeRecoverableCollaborationRejectsRunPreconditions(t *testing.T) {
	runtime, fileStore, run := newRecoverableCollaborationForTest(t)
	if err := resumeCollaborationError(runtime, "missing"); err == nil || !store.IsNotFound(err) {
		t.Fatalf("expected missing run error, got %v", err)
	}
	if _, err := fileStore.UpdateRunStatus(run.ID, domain.RunRunning, ""); err != nil {
		t.Fatal(err)
	}
	if err := resumeCollaborationError(runtime, run.ID); err == nil || !strings.Contains(err.Error(), "not recoverable") {
		t.Fatalf("expected non-recoverable error, got %v", err)
	}

	conversation, err := fileStore.CreateConversation("wrong recovery mode")
	if err != nil {
		t.Fatal(err)
	}
	snapshot := testRuntimeSnapshot()
	snapshot.Mode = ChatModeSingle
	snapshot.AutonomousLimits = nil
	single, err := fileStore.CreateRun("agent_planner", conversation.ID, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fileStore.UpdateRunStatus(single.ID, domain.RunFailedRecoverable, "interrupted"); err != nil {
		t.Fatal(err)
	}
	if err := resumeCollaborationError(runtime, single.ID); err == nil || !strings.Contains(err.Error(), "not \"multi_agent\"") {
		t.Fatalf("expected wrong mode error, got %v", err)
	}
}

func TestResumeRecoverableCollaborationUsesCompletedChildResult(t *testing.T) {
	runtime, fileStore, run := newRecoverableCollaborationForTest(t)
	planner := createRecoveryStep(t, fileStore, run, "planner", "agent_planner", "task", "approved plan")
	_ = planner
	createRecoveryStep(t, fileStore, run, "router", "agent_planner", "route", "agent_planner")
	worker := createRecoveryStep(t, fileStore, run, "worker", "agent_planner", "delegated task", "")
	worker, err := fileStore.UpdateCollaborationStep(worker.ID, domain.CollaborationStepFailed, "", "worker interrupted")
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
	child, relation, err := fileStore.CreateChildRun(domain.ChildRunRequest{
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
	if _, err := fileStore.UpdateRunDelegation(relation.ID, domain.DelegationResult{
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
	steps, err := fileStore.ListCollaborationSteps(run.ID)
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
	runtime, fileStore, run := newRecoverableCollaborationForTest(t)
	createRecoveryStep(t, fileStore, run, "planner", "agent_planner", "task", "plan")
	createRecoveryStep(t, fileStore, run, "router", "agent_planner", "route", "agent_planner")
	worker := createRecoveryStep(t, fileStore, run, "worker", "agent_planner", "work", "")
	selected, _ := findAgentByID(restoreCandidates(run.RuntimeSnapshot.CandidateAgents), "agent_planner")
	childSnapshot, err := runtime.childRuntimeSnapshot(run, selected, "delegation-created", "turn-created", worker.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, relation, err := fileStore.CreateChildRun(domain.ChildRunRequest{
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
	if _, err := fileStore.UpdateRunDelegation(relation.ID, domain.DelegationResult{Status: domain.DelegationCompleted}); err != nil {
		t.Fatal(err)
	}
	if err := resumeCollaborationError(runtime, run.ID); err == nil || !strings.Contains(err.Error(), "no child output reference") {
		t.Fatalf("expected missing output reference error, got %v", err)
	}
}

func TestResumeRecoverableCollaborationValidatesBlockedChildState(t *testing.T) {
	t.Run("parent stage missing", func(t *testing.T) {
		runtime, fileStore, run, _, relation := recoverableDelegationFixture(t)
		fault := runtimeStoreFault{FileStore: fileStore, runDelegations: []domain.RunDelegation{relation}}
		fault.runDelegations[0].ParentStageID = "missing-stage"
		runtime = NewRuntime(RuntimeOptions{Store: &fault, ModelClient: newLocalFallbackOpenAIClientForTest(), ChildRuns: runtime.childRunLimits})
		if err := resumeCollaborationError(runtime, run.ID); err == nil || !strings.Contains(err.Error(), "parent worker delegation stage not found") {
			t.Fatalf("expected missing parent stage error, got %v", err)
		}
	})

	t.Run("unsupported block reason", func(t *testing.T) {
		runtime, fileStore, run, _, relation := recoverableDelegationFixture(t)
		fault := runtimeStoreFault{FileStore: fileStore, runDelegations: []domain.RunDelegation{relation}}
		fault.runDelegations[0].Status = domain.DelegationBlocked
		fault.runDelegations[0].BlockReason = "manual_review"
		runtime = NewRuntime(RuntimeOptions{Store: &fault, ModelClient: newLocalFallbackOpenAIClientForTest(), ChildRuns: runtime.childRunLimits})
		if err := resumeCollaborationError(runtime, run.ID); err == nil || !strings.Contains(err.Error(), "unsupported block reason") {
			t.Fatalf("expected unsupported block reason error, got %v", err)
		}
	})

	t.Run("child is not recoverable", func(t *testing.T) {
		runtime, fileStore, run, _, relation := recoverableDelegationFixture(t)
		if _, err := fileStore.UpdateRunDelegation(relation.ID, domain.DelegationResult{Status: domain.DelegationBlocked, BlockReason: domain.DelegationBlockReasonChildRecoveryRequired}); err != nil {
			t.Fatal(err)
		}
		if err := resumeCollaborationError(runtime, run.ID); err == nil || !strings.Contains(err.Error(), "child run is not recoverable") {
			t.Fatalf("expected child state error, got %v", err)
		}
	})

	t.Run("child capacity exhausted", func(t *testing.T) {
		runtime, fileStore, run, child, relation := recoverableDelegationFixture(t)
		if _, err := fileStore.UpdateRunStatus(child.ID, domain.RunFailedRecoverable, "interrupted"); err != nil {
			t.Fatal(err)
		}
		if _, err := fileStore.UpdateRunDelegation(relation.ID, domain.DelegationResult{Status: domain.DelegationBlocked, BlockReason: domain.DelegationBlockReasonChildRecoveryRequired}); err != nil {
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
			baseRuntime, fileStore, run, child, relation := recoverableDelegationFixture(t)
			if test.name == "update parent run" {
				if _, err := fileStore.UpdateRunDelegation(relation.ID, domain.DelegationResult{Status: domain.DelegationCompleted, OutputRef: "run://" + child.ID}); err != nil {
					t.Fatal(err)
				}
			} else if test.name != "list delegations" {
				if _, err := fileStore.UpdateRunStatus(child.ID, domain.RunFailedRecoverable, "interrupted"); err != nil {
					t.Fatal(err)
				}
				if _, err := fileStore.UpdateRunDelegation(relation.ID, domain.DelegationResult{Status: domain.DelegationBlocked, BlockReason: domain.DelegationBlockReasonChildRecoveryRequired}); err != nil {
					t.Fatal(err)
				}
			}
			fault := runtimeStoreFault{FileStore: fileStore}
			test.fault(&fault, run, child, relation)
			runtime := NewRuntime(RuntimeOptions{Store: &fault, ModelClient: newLocalFallbackOpenAIClientForTest(), ChildRuns: baseRuntime.childRunLimits})
			if err := resumeCollaborationError(runtime, run.ID); err == nil || test.want != "" && !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected store failure, got %v", err)
			}
		})
	}
}

func recoverableDelegationFixture(t *testing.T) (*Runtime, *store.FileStore, domain.Run, domain.Run, domain.RunDelegation) {
	t.Helper()
	runtime, fileStore, run := newRecoverableCollaborationForTest(t)
	createRecoveryStep(t, fileStore, run, "planner", "agent_planner", "task", "plan")
	createRecoveryStep(t, fileStore, run, "router", "agent_planner", "route", "agent_planner")
	worker := createRecoveryStep(t, fileStore, run, "worker", "agent_planner", "work", "")
	if _, err := fileStore.UpdateCollaborationStep(worker.ID, domain.CollaborationStepFailed, "", "interrupted"); err != nil {
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
	child, relation, err := fileStore.CreateChildRun(domain.ChildRunRequest{
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
	return runtime, fileStore, run, child, relation
}

func completedDelegationForResume(relation domain.RunDelegation) domain.RunDelegation {
	relation.Status = domain.DelegationCompleted
	relation.OutputRef = "run://" + relation.ChildRunID
	return relation
}

func newRecoverableCollaborationForTest(t *testing.T) (*Runtime, *store.FileStore, domain.Run) {
	t.Helper()
	fileStore, err := store.NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := fileStore.CreateConversation("recoverable collaboration boundary")
	if err != nil {
		t.Fatal(err)
	}
	runtime := NewRuntime(RuntimeOptions{
		Store: fileStore, ModelClient: newLocalFallbackOpenAIClientForTest(), RouterMode: RouterModeQuery,
		ChildRuns: ChildRunLimits{MaxConcurrent: 1, MaxPerParent: 1, Timeout: time.Minute, SummaryMaxChars: 100,
			RunBudget: domain.RuntimeRunBudget{MaxModelCalls: 2, MaxTotalTokens: 4000}},
	})
	prepared, err := runtime.PrepareCollaborationRun(context.Background(), "agent_planner", conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	run, err := fileStore.UpdateRunStatus(prepared.Run.ID, domain.RunFailedRecoverable, "worker interrupted")
	if err != nil {
		t.Fatal(err)
	}
	return runtime, fileStore, run
}

func createRecoveryStep(t *testing.T, fileStore *store.FileStore, run domain.Run, role, agentID, input, output string) domain.CollaborationStep {
	t.Helper()
	step, err := fileStore.CreateCollaborationStep(domain.CollaborationStep{
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
	fileStore, err := store.NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	conversation, err := fileStore.CreateConversation("Autonomous test")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	runtime := NewRuntime(RuntimeOptions{
		Store: fileStore, ModelClient: newLocalFallbackOpenAIClientForTest(), RouterMode: RouterModeQuery,
		Autonomous: AutonomousLimits{
			MaxIterations: 1, MaxRuntime: time.Minute, MaxOutputChars: 60000, MaxToolCalls: 20,
		},
	})
	prepared, err := runtime.PrepareAutonomousRun(context.Background(), "", conversation.ID)
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

	steps, err := fileStore.ListCollaborationSteps(prepared.Run.ID)
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
	runEvents, err := fileStore.ListRunEvents(prepared.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := eventpkg.ValidateLifecycle(runEvents); err != nil {
		t.Fatalf("invalid autonomous event lifecycle: %v", err)
	}
}

func TestAutonomousRunCanBeCanceledBeforeLoop(t *testing.T) {
	fileStore, err := store.NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	conversation, err := fileStore.CreateConversation("Cancel test")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	runtime := NewRuntime(RuntimeOptions{
		Store: fileStore, ModelClient: newLocalFallbackOpenAIClientForTest(), RouterMode: RouterModeQuery,
		Autonomous: AutonomousLimits{
			MaxIterations: 2, MaxRuntime: time.Minute, MaxOutputChars: 60000, MaxToolCalls: 20,
		},
	})
	prepared, err := runtime.PrepareAutonomousRun(context.Background(), "", conversation.ID)
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
	run, ok, err := fileStore.GetRun(prepared.Run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if !ok || run.Status != domain.RunCanceled {
		t.Fatalf("expected canceled run, got %#v", run)
	}
}

func TestResumeAutonomousCompletesHumanInputCheckpoint(t *testing.T) {
	fileStore, err := store.NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	conversation, err := fileStore.CreateConversation("HITL test")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	runtime := NewRuntime(RuntimeOptions{
		Store: fileStore, ModelClient: newLocalFallbackOpenAIClientForTest(), RouterMode: RouterModeQuery,
		Autonomous: AutonomousLimits{
			MaxIterations: 2, MaxRuntime: time.Minute, MaxOutputChars: 60000, MaxToolCalls: 20,
		},
	})
	prepared, err := runtime.PrepareAutonomousRun(context.Background(), "", conversation.ID)
	if err != nil {
		t.Fatalf("prepare autonomous run: %v", err)
	}
	checkpoint, err := fileStore.CreateCollaborationStep(domain.CollaborationStep{
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
	if _, err := fileStore.UpdateRunStatus(prepared.Run.ID, domain.RunWaitingForUser, ""); err != nil {
		t.Fatalf("mark waiting: %v", err)
	}

	events, errs := runtime.ResumeAutonomous(context.Background(), prepared.Run.ID, "AgentFlow")
	for range events {
	}
	if err := <-errs; err != nil {
		t.Fatalf("resume autonomous: %v", err)
	}
	updated, ok, err := fileStore.GetRun(prepared.Run.ID)
	if err != nil || !ok {
		t.Fatalf("get run after resume: %v", err)
	}
	if updated.Status == domain.RunWaitingForUser {
		t.Fatalf("expected run to leave waiting_for_user")
	}
	steps, err := fileStore.ListCollaborationSteps(prepared.Run.ID)
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
	fileStore, err := store.NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	conversation, err := fileStore.CreateConversation("Recovery resume test")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	runtime := NewRuntime(RuntimeOptions{
		Store: fileStore, ModelClient: newLocalFallbackOpenAIClientForTest(), RouterMode: RouterModeQuery,
		Autonomous: AutonomousLimits{
			MaxIterations: 2, MaxRuntime: time.Minute, MaxOutputChars: 60000, MaxToolCalls: 20,
		},
	})
	prepared, err := runtime.PrepareAutonomousRun(context.Background(), "", conversation.ID)
	if err != nil {
		t.Fatalf("prepare autonomous run: %v", err)
	}
	if _, err := fileStore.CreateCollaborationStep(domain.CollaborationStep{
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
	if _, err := fileStore.UpdateRunStatus(prepared.Run.ID, domain.RunFailedRecoverable, "heartbeat expired"); err != nil {
		t.Fatalf("mark recoverable: %v", err)
	}

	events, errs := runtime.ResumeRecoverableAutonomous(context.Background(), prepared.Run.ID, "Continue after restart")
	for range events {
	}
	if err := <-errs; err != nil {
		t.Fatalf("resume recoverable autonomous: %v", err)
	}
	updated, ok, err := fileStore.GetRun(prepared.Run.ID)
	if err != nil || !ok {
		t.Fatalf("get run after resume: %v", err)
	}
	if updated.Status == domain.RunFailedRecoverable {
		t.Fatalf("expected run to leave failed_recoverable")
	}
	steps, err := fileStore.ListCollaborationSteps(prepared.Run.ID)
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
