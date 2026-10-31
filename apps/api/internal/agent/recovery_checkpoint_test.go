package agent

import (
	"errors"
	"reflect"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/testsupport/fixturestore"
)

type stopBeforeResumeStore struct {
	*fixturestore.Store
	stop bool
}

func (s *stopBeforeResumeStore) TransitionRun(request domain.RunTransition) (domain.RunTransitionResult, error) {
	if s.stop && request.EventType == domain.EventRunResumed {
		s.stop = false
		if _, err := s.Store.TransitionRun(domain.RunTransition{RunID: request.RunID, From: domain.RunFailedRecoverable, To: domain.RunCanceled, EventType: domain.EventRunCanceled}); err != nil {
			return domain.RunTransitionResult{}, err
		}
	}
	return s.Store.TransitionRun(request)
}

// Failure inventory: missing continuation boundary or a Stop that wins the CAS
// must not compensate an unfinished checkpoint before recovery is accepted.
func TestRejectedRecoveryPreservesCheckpoint(t *testing.T) {
	for _, reason := range []string{"missing router", "Stop before resume"} {
		t.Run(reason, func(t *testing.T) {
			storage := &stopBeforeResumeStore{Store: fixturestore.New()}
			runtime := newRuntimeForTest(RuntimeOptions{Store: storage}, newLocalFallbackOpenAIClientForTest())
			conversation, err := storage.CreateConversation("Synthetic checkpoint rejection")
			if err != nil {
				t.Fatal(err)
			}
			prepared, err := runtime.PrepareCollaborationRunWithContract(t.Context(), "agent_planner", conversation.ID, nil)
			if err != nil {
				t.Fatal(err)
			}
			createCompletedStage(t, runtime, prepared.Run, "planner", "agent_planner", "task", "plan")
			if reason == "Stop before resume" {
				createCompletedStage(t, runtime, prepared.Run, "router", "agent_planner", "route", "agent_planner")
			}
			step, err := storage.CreateCollaborationStep(domain.CollaborationStep{RunID: prepared.Run.ID, ConversationID: conversation.ID, Role: "worker", Status: domain.CollaborationStepRunning, Input: "synthetic work"})
			if err != nil {
				t.Fatal(err)
			}
			if err := runtime.publishStage(t.Context(), step, domain.EventStageStarted); err != nil {
				t.Fatal(err)
			}
			if _, err := storage.UpdateRunStatus(prepared.Run.ID, domain.RunFailedRecoverable, "fixture interruption"); err != nil {
				t.Fatal(err)
			}
			before, err := storage.ListStageCheckpoints(prepared.Run.ID)
			if err != nil {
				t.Fatal(err)
			}
			prior, _ := storage.ListRunEvents(prepared.Run.ID)
			storage.stop = reason == "Stop before resume"
			events, errs := runtime.ResumeRecoverableCollaboration(t.Context(), prepared.Run.ID)
			for range events {
				t.Error("rejected recovery emitted execution")
			}
			err = <-errs
			var rejected *PreExecutionRejection
			if err == nil || (!errors.As(err, &rejected) && !errors.Is(err, ErrRunStateChanged)) {
				t.Fatalf("expected pre-execution rejection: %v", err)
			}
			after, err := storage.ListStageCheckpoints(prepared.Run.ID)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("rejected recovery changed checkpoints: before=%+v after=%+v error=%v", before, after, err)
			}
			later, _ := storage.ListRunEvents(prepared.Run.ID)
			for _, item := range later[len(prior):] {
				if item.Type != domain.EventRunCanceled {
					t.Fatalf("rejected recovery appended %s", item.Type)
				}
			}
		})
	}
}
