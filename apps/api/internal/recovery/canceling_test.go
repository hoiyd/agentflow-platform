package recovery

import (
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/testsupport/fixturestore"
)

// Failure inventory: a canceled worker cannot acknowledge Stop after a crash;
// repair must settle canceled, close open scopes and preserve uncertain effects.
func TestInterruptedCancellationSettlesCanceled(t *testing.T) {
	s := fixturestore.New()
	c, _ := s.CreateConversation("interrupted cancellation")
	run, err := s.CreateRunWithContract("agent_planner", c.ID, domain.RuntimeSnapshot{SchemaVersion: domain.CurrentRuntimeSnapshotVersion, RunBudget: &domain.RuntimeRunBudget{}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, typ := range []domain.RunEventType{domain.EventRunCreated, domain.EventRunStarted} {
		if _, err = s.CreateRunEvent(domain.RunEvent{RunID: run.ID, ConversationID: c.ID, Type: typ}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = s.UpdateRunStatus(run.ID, domain.RunCanceling, ""); err != nil {
		t.Fatal(err)
	}
	// A heartbeat-free interrupted execution is stale without a wall-clock sleep.
	count, err := MarkStaleRunningRuns(s, time.Second)
	got, _, _ := s.GetRun(run.ID)
	if err != nil || count != 1 || got.Status != domain.RunCanceled {
		t.Fatalf("count=%d run=%+v err=%v", count, got, err)
	}
	events, _ := s.ListRunEvents(run.ID)
	if events[len(events)-1].Type != domain.EventRunCanceled {
		t.Fatal(events)
	}
	if err = event.ValidateLifecycle(events); err != nil {
		t.Fatal(err)
	}
	if count, err = MarkStaleRunningRuns(s, time.Second); err != nil || count != 0 {
		t.Fatalf("duplicate: %d %v", count, err)
	}
}

func TestRecoveryStatusChangedDuringPlanning(t *testing.T) {
	s := fixturestore.New()
	c, _ := s.CreateConversation("cancel races with repair")
	run, _ := s.CreateRunWithContract("agent_planner", c.ID, domain.RuntimeSnapshot{SchemaVersion: domain.CurrentRuntimeSnapshotVersion, RunBudget: &domain.RuntimeRunBudget{}}, nil)
	s.UpdateRunStatus(run.ID, domain.RunRunning, "")
	s.CreateRunEvent(domain.RunEvent{RunID: run.ID, ConversationID: c.ID, Type: domain.EventRunStarted})
	racing := &cancelDuringPlan{Store: s}
	count, err := MarkStaleRunningRuns(racing, time.Nanosecond)
	got, _, _ := s.GetRun(run.ID)
	if err != nil || count != 0 || got.Status != domain.RunCanceling {
		t.Fatalf("stale status plan applied: count=%d run=%+v err=%v", count, got, err)
	}
	count, err = MarkStaleRunningRuns(s, time.Nanosecond)
	events, _ := s.ListRunEvents(run.ID)
	if err != nil || count != 1 || events[len(events)-1].Type != domain.EventRunCanceled {
		t.Fatalf("retry: %d %+v %v", count, events, err)
	}
}

type cancelDuringPlan struct{ *fixturestore.Store }

func (s *cancelDuringPlan) ListRunEvents(id string) ([]domain.RunEvent, error) {
	if _, err := s.UpdateRunStatus(id, domain.RunCanceling, ""); err != nil {
		return nil, err
	}
	return s.Store.ListRunEvents(id)
}
