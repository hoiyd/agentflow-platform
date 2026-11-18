package agent

import (
	"errors"
	"sync/atomic"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/testsupport/fixturestore"
)

// Failure inventory: stale Run status and wrong frozen mode must reject
// without writing Run, Stage, or lifecycle events.
func TestRecoveryPreflightRejectsWithoutMutation(t *testing.T) {
	for _, entry := range []string{"autonomous", "collaboration", "continue"} {
		t.Run(entry, func(t *testing.T) {
			storage := fixturestore.New()
			runtime := newRuntimeForTest(RuntimeOptions{Store: storage}, newHTTPFixtureClientForTest())
			conv, err := storage.CreateConversation("Preflight rejection")
			if err != nil {
				t.Fatal(err)
			}
			prepared, err := runtime.PrepareChatRunWithContract(t.Context(), "agent_planner", conv.ID, nil)
			if err != nil {
				t.Fatal(err)
			}
			before, _, _ := storage.GetRun(prepared.Run.ID)
			priorEvents, _ := storage.ListRunEvents(prepared.Run.ID)
			var events <-chan domain.RunEvent
			var errs <-chan error
			switch entry {
			case "autonomous":
				events, errs = runtime.ResumeRecoverableAutonomous(t.Context(), before.ID, "")
			case "collaboration":
				events, errs = runtime.ResumeRecoverableCollaboration(t.Context(), before.ID)
			case "continue":
				events, errs = runtime.ContinueCollaboration(t.Context(), before.ID, "plan", domain.AgentRoutingRequirements{})
			}
			for range events {
				t.Error("preflight emitted execution event")
			}
			var rejected *PreExecutionRejection
			if err = <-errs; !errors.As(err, &rejected) {
				t.Fatalf("untyped preflight error: %v", err)
			}
			after, _, _ := storage.GetRun(before.ID)
			afterEvents, _ := storage.ListRunEvents(before.ID)
			steps, _ := storage.ListCollaborationSteps(before.ID)
			if after.Status != before.Status || !after.UpdatedAt.Equal(before.UpdatedAt) || len(afterEvents) != len(priorEvents) || len(steps) != 0 {
				t.Fatalf("preflight mutated Run: before=%+v after=%+v events=%d/%d stages=%d", before, after, len(priorEvents), len(afterEvents), len(steps))
			}
		})
	}
}

func TestAutonomousForwardedExecutionFailureIsNotPreflight(t *testing.T) {
	storage := &postResumeReadFailure{Store: fixturestore.New()}
	runtime := newRuntimeForTest(RuntimeOptions{Store: storage, CheckpointProvider: &checkpointProviderStub{}}, newHTTPFixtureClientForTest())
	conv, err := storage.CreateConversation("Post-resume failure")
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := runtime.PrepareAutonomousRunWithContract(t.Context(), "agent_planner", conv.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = storage.UpdateRunStatus(prepared.Run.ID, domain.RunFailedRecoverable, ""); err != nil {
		t.Fatal(err)
	}
	storage.failRunning.Store(true)
	events, errs := runtime.ResumeRecoverableAutonomous(t.Context(), prepared.Run.ID, "")
	accepted := false
	for event := range events {
		accepted = accepted || event.Type == domain.EventRunStarted
	}
	err = <-errs
	var rejected *PreExecutionRejection
	if !accepted || err == nil || errors.As(err, &rejected) {
		t.Fatalf("execution failure became preflight: accepted=%v error=%v", accepted, err)
	}
}

type postResumeReadFailure struct {
	*fixturestore.Store
	failRunning atomic.Bool
}

func (s *postResumeReadFailure) GetRun(id string) (domain.Run, bool, error) {
	run, found, err := s.Store.GetRun(id)
	if err == nil && found && s.failRunning.Load() && run.Status == domain.RunRunning {
		return domain.Run{}, false, errors.New("not waiting for user input: execution read failed")
	}
	return run, found, err
}
