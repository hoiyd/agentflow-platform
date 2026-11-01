package recovery

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/testsupport/fixturestore"
)

// Failure inventory: fresh orphans are repaired at expiry; post-start Runs and
// renewed heartbeats are excluded even when the recheck is late; Close cancels it.
func TestColdStartBoundedRecheck(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := fixturestore.New()
		create := func() domain.Run {
			c, _ := s.CreateConversation("cold start")
			run, err := s.CreateRunWithContract("agent_planner", c.ID, domain.RuntimeSnapshot{SchemaVersion: domain.CurrentRuntimeSnapshotVersion, RunBudget: &domain.RuntimeRunBudget{}}, nil)
			if err != nil {
				t.Fatal(err)
			}
			run, err = s.UpdateRunStatus(run.ID, domain.RunRunning, "")
			if err != nil {
				t.Fatal(err)
			}
			return run
		}
		orphan, renewed := create(), create()
		time.Sleep(time.Second) // Virtual time, not a scheduler/startup assumption.
		repair, err := PrepareColdStart(s, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		repair.Start()
		synctest.Wait()
		live := create()
		if _, err = s.UpdateRunHeartbeat(renewed.ID); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Minute)
		synctest.Wait()
		for _, item := range []struct {
			id   string
			want domain.RunStatus
		}{{orphan.ID, domain.RunFailedRecoverable}, {renewed.ID, domain.RunRunning}, {live.ID, domain.RunRunning}} {
			got, _, err := s.GetRun(item.id)
			if err != nil || got.Status != item.want {
				t.Fatalf("%s: %+v %v", item.id, got, err)
			}
		}
		if err = repair.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
	})
}

func TestColdStartCloseAndErrors(t *testing.T) {
	if _, err := PrepareColdStart(&recoveryTestStore{listErr: errors.New("offline")}, time.Second); err == nil {
		t.Fatal("lost scan failure")
	}
	synctest.Test(t, func(t *testing.T) {
		s := fixturestore.New()
		c, _ := s.CreateConversation("close before recheck")
		run, _ := s.CreateRunWithContract("agent_planner", c.ID, domain.RuntimeSnapshot{SchemaVersion: domain.CurrentRuntimeSnapshotVersion, RunBudget: &domain.RuntimeRunBudget{}}, nil)
		s.UpdateRunStatus(run.ID, domain.RunRunning, "")
		time.Sleep(time.Second)
		repair, err := PrepareColdStart(s, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		repair.Start()
		if err = repair.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Minute)
		synctest.Wait()
		got, _, _ := s.GetRun(run.ID)
		if got.Status != domain.RunRunning {
			t.Fatal(got)
		}
	})
}
