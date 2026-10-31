package store

import (
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/testsupport/pgfixture"
)

func TestInitialRunLifecyclePrefix(t *testing.T) {
	for _, scenario := range []struct {
		name     string
		from, to domain.RunStatus
		kind     domain.RunEventType
	}{
		{"ordinary Start", domain.RunQueued, domain.RunRunning, domain.EventRunStarted},
		{"durable input Start", domain.RunRunning, domain.RunRunning, domain.EventRunStarted},
		{"Stop before durable input Start", domain.RunRunning, domain.RunCanceling, domain.EventRunCancelRequested},
		{"queued Stop", domain.RunQueued, domain.RunCanceled, domain.EventRunCanceled},
		{"queued preparation failure", domain.RunQueued, domain.RunFailed, domain.EventRunFailed},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			run := domain.Run{ID: "run-init", ConversationID: "conv-init", Status: scenario.from}
			request := domain.RunTransition{RunID: run.ID, From: scenario.from, To: scenario.to, EventType: scenario.kind}
			result, err := PrepareRunTransition(run, request, 0, time.Now().UTC())
			if err != nil || !result.Applied {
				t.Fatalf("initial transition rejected: applied=%t %v", result.Applied, err)
			}
			if len(result.Events) != 2 || result.Events[0].Type != domain.EventRunCreated || result.Events[1].Type != scenario.kind {
				t.Fatalf("missing atomic created prefix: %+v", result.Events)
			}
			for i, item := range result.Events {
				if item.Sequence != int64(i+1) || item.StageID != "" || item.TurnID != "" {
					t.Errorf("invalid initialization identity/order: %+v", item)
				}
			}
			if failures := event.CheckRuntimeInvariants(result.Run, result.Events); len(failures) != 0 {
				t.Errorf("initialization orphaned: %+v", failures)
			}
			stale := run
			stale.Status = domain.RunCanceled
			refused, err := PrepareRunTransition(stale, request, 0, time.Now().UTC())
			if err != nil || refused.Applied || len(refused.Events) != 0 {
				t.Errorf("stale writer synthesized lifecycle: %+v %v", refused, err)
			}
		})
	}
}

func TestPostgresRunInitializationPrefixRollback(t *testing.T) {
	s, err := NewPostgresStore(pgfixture.DatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	conversation, err := s.CreateConversation("Atomic canceled-before-start lifecycle")
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.CreateRunWithContract("agent_planner", conversation.ID, testRuntimeSnapshot(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`CREATE FUNCTION reject_initial_cancel() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.type='run.canceled' THEN RAISE EXCEPTION 'initial cancellation write failed'; END IF; RETURN NEW; END $$;
 CREATE TRIGGER reject_initial_cancel BEFORE INSERT ON run_events FOR EACH ROW EXECUTE FUNCTION reject_initial_cancel()`); err != nil {
		t.Fatal(err)
	}
	request := domain.RunTransition{RunID: run.ID, From: domain.RunQueued, To: domain.RunCanceled, EventType: domain.EventRunCanceled}
	if _, err := s.TransitionRun(request); err == nil {
		t.Fatal("expected failure after initialization prefix insertion")
	}
	current, _, err := s.GetRun(run.ID)
	if err != nil || current.Status != domain.RunQueued {
		t.Fatalf("failed initialization changed status: %+v %v", current, err)
	}
	items, err := s.ListRunEvents(run.ID)
	if err != nil || len(items) != 0 {
		t.Fatalf("failed initialization leaked prefix: %+v %v", items, err)
	}
	if _, err := s.db.Exec(`DROP TRIGGER reject_initial_cancel ON run_events`); err != nil {
		t.Fatal(err)
	}
	result, err := s.TransitionRun(request)
	if err != nil || !result.Applied || len(result.Events) != 2 || result.Events[0].Type != domain.EventRunCreated || result.Events[1].Type != domain.EventRunCanceled {
		t.Fatalf("initial canceled lifecycle not paired: %+v %v", result, err)
	}
	if failures := event.CheckRuntimeInvariants(result.Run, result.Events); len(failures) != 0 {
		t.Fatalf("committed initialization invalid: %+v", failures)
	}
}
