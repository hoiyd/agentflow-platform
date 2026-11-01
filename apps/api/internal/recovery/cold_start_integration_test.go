package recovery

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/testsupport/pgfixture"
)

func TestPostgresColdStartReportsStoreFailure(t *testing.T) {
	url := pgfixture.DatabaseURL(t)
	s, err := store.NewPostgresStore(url)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	c, err := s.CreateConversation("cold-start storage failure")
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.CreateRunWithContract("agent_planner", c.ID, domain.RuntimeSnapshot{SchemaVersion: domain.CurrentRuntimeSnapshotVersion, RunBudget: &domain.RuntimeRunBudget{}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpdateRunStatus(run.ID, domain.RunRunning, ""); err != nil {
		t.Fatal(err)
	}
	repair, err := PrepareColdStart(s, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	// Exercise the deferred persistence failure without waiting for a wall-clock
	// expiry. The virtual-clock test owns the scheduling contract.
	repair.due = time.Now()
	repair.Start()
	<-repair.done
	if err = repair.Close(context.Background()); err == nil {
		t.Fatal("deferred failure reported as successful drain")
	}
	if _, err = PrepareColdStart(s, time.Hour); err == nil {
		t.Fatal("startup accepted a failed Store scan")
	}
}

func TestPostgresColdStartRecheck(t *testing.T) {
	url := pgfixture.DatabaseURL(t)
	s, err := store.NewPostgresStore(url)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	create := func(status domain.RunStatus) domain.Run {
		c, err := s.CreateConversation("synthetic cold-start recovery")
		if err != nil {
			t.Fatal(err)
		}
		r, err := s.CreateRunWithContract("agent_planner", c.ID, domain.RuntimeSnapshot{SchemaVersion: domain.CurrentRuntimeSnapshotVersion, RunBudget: &domain.RuntimeRunBudget{}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, typ := range []domain.RunEventType{domain.EventRunCreated, domain.EventRunStarted} {
			if _, err = s.CreateRunEvent(domain.RunEvent{RunID: r.ID, ConversationID: c.ID, Type: typ}); err != nil {
				t.Fatal(err)
			}
		}
		if _, err = s.UpdateRunStatus(r.ID, domain.RunRunning, ""); err != nil {
			t.Fatal(err)
		}
		if status == domain.RunCanceling {
			if _, err = s.UpdateRunStatus(r.ID, status, ""); err != nil {
				t.Fatal(err)
			}
		}
		return r
	}
	orphan, stopped, ordinary := create(domain.RunRunning), create(domain.RunCanceling), create(domain.RunRunning)
	step, err := s.CreateCollaborationStep(domain.CollaborationStep{RunID: orphan.ID, ConversationID: orphan.ConversationID, Role: "worker", Status: domain.CollaborationStepRunning})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateRunEvent(domain.RunEvent{RunID: orphan.ID, ConversationID: orphan.ConversationID, StageID: step.ID, Type: domain.EventStageStarted}); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.BeginToolEffect(domain.ToolEffectRecord{IdempotencyKey: "uncertain", RunID: orphan.ID, StageID: step.ID, ToolCallID: "call", ToolName: "external_writer", RequestHash: "hash"}); err != nil {
		t.Fatal(err)
	}
	scan, err := PrepareColdStart(s, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	got, _, _ := s.GetRun(orphan.ID)
	if got.Status != domain.RunRunning {
		t.Fatal("fresh execution repaired before expiry")
	}
	// Stop may win after the expiry scan has read running but before planning.
	// The bounded recheck must re-read the resulting canceling state.
	scan.store = &cancelBeforeRecoveryRead{Store: s, runID: orphan.ID, cancel: func() {
		if _, err := s.UpdateRunStatus(orphan.ID, domain.RunCanceling, ""); err != nil {
			t.Fatal(err)
		}
	}}
	live := create(domain.RunRunning)
	// Control durable timestamps explicitly; the virtual-clock test separately
	// proves expiry scheduling. No test depends on DB/process startup duration.
	if _, err = db.Exec("UPDATE runs SET heartbeat_at=NOW()-INTERVAL '2 hours'"); err != nil {
		t.Fatal(err)
	}
	stalePlan, err := s.RepairInterruptedRun(domain.InterruptedRunRepair{RunID: stopped.ID, ExpectedStatus: domain.RunRunning, StaleBefore: scan.cutoff})
	if err != nil || stalePlan.Applied || stalePlan.Run.Status != domain.RunCanceling {
		t.Fatalf("stale status plan: %+v %v", stalePlan, err)
	}
	if err = scan.recheck(); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		run  domain.Run
		want domain.RunStatus
	}{{orphan, domain.RunCanceled}, {stopped, domain.RunCanceled}, {ordinary, domain.RunFailedRecoverable}, {live, domain.RunRunning}} {
		got, _, err := s.GetRun(item.run.ID)
		if err != nil || got.Status != item.want {
			t.Fatalf("%+v %v", got, err)
		}
		events, _ := s.ListRunEvents(got.ID)
		if err = event.ValidateLifecycle(events); err != nil {
			t.Fatal(err)
		}
	}
	effects, err := s.ListToolEffects(orphan.ID)
	if err != nil || len(effects) != 1 || effects[0].Status != domain.ToolEffectNeedsReconciliation {
		t.Fatalf("%+v %v", effects, err)
	}
	before, _ := s.ListRunEvents(orphan.ID)
	if err = scan.recheck(); err != nil {
		t.Fatal(err)
	}
	after, _ := s.ListRunEvents(orphan.ID)
	if len(after) != len(before) {
		t.Fatal("duplicate recovery facts")
	}
}

type cancelBeforeRecoveryRead struct {
	Store
	runID  string
	cancel func()
}

func (s *cancelBeforeRecoveryRead) ListRunEvents(id string) ([]domain.RunEvent, error) {
	if id == s.runID && s.cancel != nil {
		cancel := s.cancel
		s.cancel = nil
		cancel()
	}
	return s.Store.ListRunEvents(id)
}
