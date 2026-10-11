package agent

import (
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/testsupport/fixturestore"
	"context"
	"strings"
	"testing"
)

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
	runtime := newRuntimeForTest(RuntimeOptions{Store: fixtureStore, RouterMode: RouterModeQuery}, newLocalFallbackOpenAIClientForTest())
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
