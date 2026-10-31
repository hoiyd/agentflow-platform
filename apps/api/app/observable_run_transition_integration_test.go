package app

import (
	"database/sql"
	"fmt"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/testsupport/pgfixture"
)

// A Run UPDATE failure after event insertion must roll both writes back and
// produce no subscriber/telemetry notification. A duplicate also publishes none.
func TestObservableLifecyclePublishesOnlyAtomicCommits(t *testing.T) {
	url := pgfixture.DatabaseURL(t)
	base, err := store.NewPostgresStore(url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = base.Close() })
	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	conversation, err := base.CreateConversation("Synthetic committed lifecycle")
	if err != nil {
		t.Fatal(err)
	}
	run, err := base.CreateRunWithContract("agent_planner", conversation.ID, domain.RuntimeSnapshot{SchemaVersion: domain.CurrentRuntimeSnapshotVersion, RunBudget: &domain.RuntimeRunBudget{}, Mode: "single"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	observed := newObservableStore(base, event.NewHub(8))
	var delivered []domain.RunEvent
	observed.observe = func(item domain.RunEvent) {
		current, found, err := base.GetRun(run.ID)
		if err != nil || !found || string(current.Status) != fmt.Sprint(item.Payload["status"]) && item.Type != domain.EventRunCreated {
			t.Fatalf("publication before status commit: %+v %v", current, err)
		}
		items, err := base.ListRunEvents(run.ID)
		if err != nil || len(items) < int(item.Sequence) || items[item.Sequence-1].ID != item.ID {
			t.Fatalf("publication before event commit: %+v %v", item, err)
		}
		delivered = append(delivered, item)
	}
	start := domain.RunTransition{RunID: run.ID, From: domain.RunQueued, To: domain.RunRunning, EventType: domain.EventRunStarted}
	if _, err := observed.TransitionRun(start); err != nil {
		t.Fatal(err)
	}
	if len(delivered) != 2 {
		t.Fatalf("startup publication count: %d", len(delivered))
	}
	_, err = db.Exec(`CREATE FUNCTION reject_run_completion() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.status='completed' THEN RAISE EXCEPTION 'injected Run UPDATE failure'; END IF; RETURN NEW; END $$;
	CREATE TRIGGER reject_run_completion BEFORE UPDATE ON runs FOR EACH ROW EXECUTE FUNCTION reject_run_completion()`)
	if err != nil {
		t.Fatal(err)
	}
	complete := domain.RunTransition{RunID: run.ID, From: domain.RunRunning, To: domain.RunCompleted, EventType: domain.EventRunCompleted}
	if _, err := observed.TransitionRun(complete); err == nil {
		t.Fatal("expected Run UPDATE failure")
	}
	current, _, err := base.GetRun(run.ID)
	if err != nil || current.Status != domain.RunRunning {
		t.Fatalf("status escaped rollback: %+v %v", current, err)
	}
	items, err := base.ListRunEvents(run.ID)
	if err != nil || len(items) != 2 || len(delivered) != 2 {
		t.Fatalf("event/publication escaped rollback: %d/%d %v", len(items), len(delivered), err)
	}
	if _, err := db.Exec(`DROP TRIGGER reject_run_completion ON runs`); err != nil {
		t.Fatal(err)
	}
	if _, err := observed.TransitionRun(complete); err != nil {
		t.Fatal(err)
	}
	if _, err := observed.TransitionRun(complete); err != nil {
		t.Fatal(err)
	}
	if len(delivered) != 3 || delivered[2].Type != domain.EventRunCompleted {
		t.Fatalf("completion/duplicate publication: %+v", delivered)
	}
}
