package agent

import (
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/testsupport/fixturestore"
	"context"
	"strings"
	"testing"
	"time"
)

func TestResumeAutonomousCompletesHumanInputCheckpoint(t *testing.T) {
	fixtureStore := fixturestore.New()

	conversation, err := fixtureStore.CreateConversation("HITL test")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	runtime := newRuntimeForTest(RuntimeOptions{Store: fixtureStore, RouterMode: RouterModeQuery, Autonomous: AutonomousLimits{
		MaxIterations: 2, MaxRuntime: time.Minute, MaxOutputChars: 60000, MaxToolCalls: 20,
	}}, newLocalFallbackOpenAIClientForTest())
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
	runtime := newRuntimeForTest(RuntimeOptions{Store: fixtureStore, RouterMode: RouterModeQuery, Autonomous: AutonomousLimits{
		MaxIterations: 2, MaxRuntime: time.Minute, MaxOutputChars: 60000, MaxToolCalls: 20,
	}}, newLocalFallbackOpenAIClientForTest())
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
