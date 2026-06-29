package temporalrun

import (
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/agent"
	"agentflow-platform/apps/api/internal/openai"
	"agentflow-platform/apps/api/internal/store"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
)

func TestAgentRunWorkflowExecutesAutonomousRunActivity(t *testing.T) {
	fileStore, err := store.NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	conversation, err := fileStore.CreateConversation("Temporal demo")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	runtime := agent.NewRuntimeWithRouterModeAndLimits(fileStore, openai.NewClient("", "", ""), nil, agent.RouterModeQuery, agent.AutonomousLimits{
		MaxIterations:  1,
		MaxRuntime:     time.Minute,
		MaxOutputChars: 20000,
		MaxToolCalls:   10,
	})
	prepared, err := runtime.PrepareAutonomousRun(t.Context(), "agent_planner", conversation.ID)
	if err != nil {
		t.Fatalf("prepare run: %v", err)
	}
	if _, err := fileStore.UpdateRunRuntime(prepared.Run.ID, RuntimeTemporal, "agentflow-run-"+prepared.Run.ID, "workflow-run-test", WorkflowStatusRunning); err != nil {
		t.Fatalf("set run runtime: %v", err)
	}

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	activities := &Activities{Runtime: runtime, Store: fileStore}
	env.RegisterWorkflow(AgentRunWorkflow)
	env.RegisterActivityWithOptions(activities.ExecuteAutonomousRunActivity, activity.RegisterOptions{Name: ExecuteAutonomousRunActivityName})
	env.ExecuteWorkflow(AgentRunWorkflow, AgentRunWorkflowInput{
		RunID:          prepared.Run.ID,
		ConversationID: conversation.ID,
		AgentID:        prepared.Run.AgentID,
		Task:           "Produce a short durable workflow demo answer.",
	})

	if !env.IsWorkflowCompleted() {
		t.Fatal("expected workflow to complete")
	}
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow error: %v", err)
	}
	run, ok, err := fileStore.GetRun(prepared.Run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if !ok {
		t.Fatal("expected run")
	}
	if run.Runtime != RuntimeTemporal || run.WorkflowStatus != WorkflowStatusCompleted || run.Status != "completed" {
		t.Fatalf("expected completed temporal run, got %#v", run)
	}
	replay, ok, err := fileStore.GetRunReplay(run.ID)
	if err != nil {
		t.Fatalf("get replay: %v", err)
	}
	if !ok {
		t.Fatal("expected replay")
	}
	if len(replay.Steps) == 0 || len(replay.Events) == 0 {
		t.Fatalf("expected replay steps and trace events, got steps=%d events=%d", len(replay.Steps), len(replay.Events))
	}
}
