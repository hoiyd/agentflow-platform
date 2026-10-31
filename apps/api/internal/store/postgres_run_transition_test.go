package store

import (
	"encoding/json"
	"strings"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/testsupport/pgfixture"
)

// Failure inventory: stale/duplicate writers append nothing; event insertion
// failure rolls back status; terminal states cannot restart; startup is idempotent.
func TestPostgresRunTransitionAtomicity(t *testing.T) {
	s, err := NewPostgresStore(pgfixture.DatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	conversation, err := s.CreateConversation("Synthetic atomic lifecycle")
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.CreateRunWithContract("agent_planner", conversation.ID, testRuntimeSnapshot(), nil)
	if err != nil {
		t.Fatal(err)
	}
	start := domain.RunTransition{RunID: run.ID, From: domain.RunQueued, To: domain.RunRunning, EventType: domain.EventRunStarted}
	for _, target := range []struct {
		status domain.RunStatus
		kind   domain.RunEventType
	}{{domain.RunCompleted, domain.EventRunCompleted}, {domain.RunWaitingForUser, domain.EventRunWaitingForUser}} {
		if _, err := s.TransitionRun(domain.RunTransition{RunID: run.ID, From: domain.RunQueued, To: target.status, EventType: target.kind}); err == nil {
			t.Fatalf("queued Run accepted %s before startup", target.kind)
		}
	}
	started, err := s.TransitionRun(start)
	if err != nil || !started.Applied || len(started.Events) != 2 {
		t.Fatalf("start: %+v %v", started, err)
	}
	duplicate, err := s.TransitionRun(start)
	if err != nil || duplicate.Applied || len(duplicate.Events) != 0 {
		t.Fatalf("duplicate: %+v %v", duplicate, err)
	}
	_, err = s.db.Exec(`CREATE FUNCTION reject_lifecycle_event() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.type='run.completed' THEN RAISE EXCEPTION 'injected lifecycle insertion failure'; END IF; RETURN NEW; END $$;
	CREATE TRIGGER reject_lifecycle_event BEFORE INSERT ON run_events FOR EACH ROW EXECUTE FUNCTION reject_lifecycle_event()`)
	if err != nil {
		t.Fatal(err)
	}
	complete := domain.RunTransition{RunID: run.ID, From: domain.RunRunning, To: domain.RunCompleted, EventType: domain.EventRunCompleted}
	if _, err := s.TransitionRun(complete); err == nil {
		t.Fatal("expected injected persistence failure")
	}
	current, _, err := s.GetRun(run.ID)
	if err != nil || current.Status != domain.RunRunning {
		t.Fatalf("status survived rollback: %+v %v", current, err)
	}
	events, err := s.ListRunEvents(run.ID)
	if err != nil || len(events) != 2 {
		t.Fatalf("events survived rollback: %+v %v", events, err)
	}
	if _, err := s.db.Exec(`DROP TRIGGER reject_lifecycle_event ON run_events`); err != nil {
		t.Fatal(err)
	}
	completed, err := s.TransitionRun(complete)
	if err != nil || !completed.Applied || completed.Run.Status != domain.RunCompleted {
		t.Fatalf("complete: %+v %v", completed, err)
	}
	stale, err := s.TransitionRun(domain.RunTransition{RunID: run.ID, From: domain.RunRunning, To: domain.RunCanceling, EventType: domain.EventRunCancelRequested})
	if err != nil || stale.Applied || stale.Run.Status != domain.RunCompleted {
		t.Fatalf("stale: %+v %v", stale, err)
	}
	if _, err := s.TransitionRun(domain.RunTransition{RunID: run.ID, From: domain.RunCompleted, To: domain.RunRunning, EventType: domain.EventRunResumed}); err == nil {
		t.Fatal("terminal run restarted")
	}
	if _, err := s.TransitionRun(domain.RunTransition{RunID: run.ID, From: domain.RunCompleted, To: domain.RunFailed, EventType: domain.EventRunCompleted}); err == nil {
		t.Fatal("mismatched lifecycle event accepted")
	}
	events, err = s.ListRunEvents(run.ID)
	if err != nil || len(events) != 3 || events[2].Type != domain.EventRunCompleted || events[2].Sequence != 3 {
		t.Fatalf("committed history: %+v %v", events, err)
	}

	t.Run("transition preserves event redaction", func(t *testing.T) {
		run, err := s.CreateRunWithContract("agent_planner", conversation.ID, testRuntimeSnapshot(), nil)
		if err != nil {
			t.Fatal(err)
		}
		_, err = s.TransitionRun(domain.RunTransition{RunID: run.ID, From: domain.RunQueued, To: domain.RunRunning, EventType: domain.EventRunStarted, Payload: map[string]any{"authorization": "Bearer synthetic-sensitive-token", "safe": "visible"}})
		if err != nil {
			t.Fatal(err)
		}
		events, err := s.ListRunEvents(run.ID)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(events)
		if err != nil || strings.Contains(string(encoded), "synthetic-sensitive-token") || events[1].Payload["safe"] != "visible" {
			t.Fatalf("transition bypassed redaction: %s %v", encoded, err)
		}
	})
}
