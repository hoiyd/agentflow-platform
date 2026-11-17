package agent

import (
	"context"
	"errors"
	"testing"

	"agentflow-platform/apps/api/internal/approval"
	"agentflow-platform/apps/api/internal/clarification"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/testsupport/fixturestore"
)

type stagePersistenceFaults struct {
	*fixturestore.Store
	checkpointError error
	updateError     error
}

func (s *stagePersistenceFaults) SaveStageCheckpoint(value domain.StageCheckpoint) (domain.StageCheckpoint, error) {
	if value.Status == domain.CheckpointNeedsReconciliation && s.checkpointError != nil {
		return domain.StageCheckpoint{}, s.checkpointError
	}
	return s.Store.SaveStageCheckpoint(value)
}

func (s *stagePersistenceFaults) UpdateCollaborationStep(id string, status domain.CollaborationStepStatus, output, message string) (domain.CollaborationStep, error) {
	if s.updateError != nil {
		return domain.CollaborationStep{}, s.updateError
	}
	return s.Store.UpdateCollaborationStep(id, status, output, message)
}

// Failure inventory: both execution and persistence errors survive; cancellation
// records a terminal checkpoint, while clarification/approval waits do not fail it.
func TestStageFailurePersistence(t *testing.T) {
	for _, path := range []string{"execution", "resume"} {
		for _, scenario := range []string{"failure", "checkpoint failure", "update failure", "canceled", "clarification", "approval"} {
			t.Run(path+"/"+scenario, func(t *testing.T) {
				storage := &stagePersistenceFaults{Store: fixturestore.New()}
				runtime := newRuntimeForTest(RuntimeOptions{Store: storage}, newHTTPFixtureClientForTest())
				conversation, err := storage.CreateConversation("Synthetic Stage persistence")
				if err != nil {
					t.Fatal(err)
				}
				prepared, err := runtime.PrepareCollaborationRunWithContract(t.Context(), "agent_planner", conversation.ID, nil)
				if err != nil {
					t.Fatal(err)
				}
				step, err := storage.CreateCollaborationStep(domain.CollaborationStep{RunID: prepared.Run.ID, ConversationID: conversation.ID, Role: "worker", Status: domain.CollaborationStepRunning, Output: "partial output"})
				if err != nil {
					t.Fatal(err)
				}
				if err := runtime.publishStage(t.Context(), step, domain.EventStageStarted); err != nil {
					t.Fatal(err)
				}
				cause, persistenceErr := errors.New("synthetic execution failure"), errors.New("synthetic persistence failure")
				ctx := t.Context()
				waiting := false
				switch scenario {
				case "checkpoint failure":
					storage.checkpointError = persistenceErr
				case "update failure":
					storage.updateError = persistenceErr
				case "canceled":
					if _, err := storage.UpdateRunStatus(prepared.Run.ID, domain.RunCanceling, ""); err != nil {
						t.Fatal(err)
					}
					var cancel context.CancelFunc
					ctx, cancel = context.WithCancel(ctx)
					cancel()
					cause = errRunCanceled
				case "clarification":
					cause, waiting = clarification.Pending, true
				case "approval":
					cause, waiting = approval.Pending, true
				}
				events := make(chan domain.RunEvent, 1)
				if path == "execution" {
					err = runtime.failCollaborationStage(ctx, events, step, cause)
					if !errors.Is(err, cause) {
						t.Fatalf("execution cause lost: %v", err)
					}
				} else {
					err = runtime.failResumedStage(ctx, prepared.Run.ID, step.ID, cause)
				}
				if (scenario == "checkpoint failure" || scenario == "update failure") && !errors.Is(err, persistenceErr) {
					t.Fatalf("persistence cause lost: %v", err)
				}
				steps, err := storage.ListCollaborationSteps(prepared.Run.ID)
				if err != nil {
					t.Fatal(err)
				}
				checkpoint, found, err := storage.GetStageCheckpoint(prepared.Run.ID, step.ID)
				if err != nil || !found {
					t.Fatalf("checkpoint missing: %v", err)
				}
				if waiting || scenario == "update failure" {
					if steps[0].Status != domain.CollaborationStepRunning || checkpoint.Status != domain.CheckpointExecuting || len(events) != 0 {
						t.Fatalf("uncommitted termination changed state: step=%+v checkpoint=%+v", steps[0], checkpoint)
					}
					return
				}
				if steps[0].Status != domain.CollaborationStepFailed || steps[0].Output != step.Output {
					t.Fatalf("failure lost stage state: %+v", steps[0])
				}
				if scenario != "checkpoint failure" && checkpoint.Status != domain.CheckpointNeedsReconciliation {
					t.Fatalf("terminal checkpoint not persisted: %+v", checkpoint)
				}
				facts, err := storage.ListRunEvents(prepared.Run.ID)
				if err != nil {
					t.Fatal(err)
				}
				kind := domain.EventStageFailed
				if scenario == "canceled" {
					kind = domain.EventStageCanceled
				}
				found = false
				for _, event := range facts {
					if event.StageID == step.ID && event.Type == kind {
						found = true
					}
				}
				if !found {
					t.Fatalf("terminal event %s missing", kind)
				}
				if path == "execution" && scenario != "checkpoint failure" {
					if live := <-events; live.Type != kind {
						t.Fatalf("live/durable termination disagree: live=%s durable=%s", live.Type, kind)
					}
				}
			})
		}
	}
}
