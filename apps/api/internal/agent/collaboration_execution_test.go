package agent

import (
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/taskstate"
	"agentflow-platform/apps/api/internal/testsupport/fixturestore"
	"agentflow-platform/apps/api/internal/tool"
	"context"
	"strings"
	"testing"
)

func TestPreparedRunsUseRequestedAgent(t *testing.T) {
	fixtureStore := fixturestore.New()

	runtime := newRuntimeForTest(RuntimeOptions{Store: fixtureStore}, newLocalFallbackOpenAIClientForTest())
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
	runtime := newRuntimeForTest(RuntimeOptions{Store: fixtureStore, RouterMode: RouterModeQuery, KnowledgeRetriever: retriever}, newLocalFallbackOpenAIClientForTest())
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
