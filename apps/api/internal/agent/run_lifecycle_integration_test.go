package agent

import (
	"errors"
	"sync"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/identity"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/testsupport/pgfixture"
)

// Failure inventory: a stale Stop must not regress completion, a late completion
// must not undo Stop, and repeating either command must not duplicate facts.
type staleStopStore struct {
	RuntimeStore
	once     sync.Once
	complete func() error
}

func (s *staleStopStore) GetRun(id string) (domain.Run, bool, error) {
	run, found, err := s.RuntimeStore.GetRun(id)
	if err == nil && found {
		s.once.Do(func() { err = s.complete() })
	}
	return run, found, err
}

func TestRunLifecyclePostgresCompletionAndStop(t *testing.T) {
	storage, err := store.NewPostgresStore(pgfixture.DatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	workspace, err := storage.DefaultWorkspace(t.Context(), identity.SuperUserID)
	if err != nil {
		t.Fatal(err)
	}
	agent, err := storage.ForWorkspace(domain.NewWorkspaceScope(workspace)).CreateAgent(domain.Agent{Name: "Lifecycle fixture"})
	if err != nil {
		t.Fatal(err)
	}
	newRun := func(t *testing.T) domain.Run {
		t.Helper()
		conversation, err := storage.CreateConversationInWorkspace(workspace, "Synthetic lifecycle regression")
		if err != nil {
			t.Fatal(err)
		}
		run, err := storage.CreateRunWithContract(agent.ID, conversation.ID, testRuntimeSnapshot(), nil)
		if err != nil {
			t.Fatal(err)
		}
		started, err := storage.TransitionRun(domain.RunTransition{RunID: run.ID, From: domain.RunQueued, To: domain.RunRunning, EventType: domain.EventRunStarted})
		if err != nil {
			t.Fatal(err)
		}
		return started.Run
	}
	t.Run("completion wins stale Stop", func(t *testing.T) {
		run := newRun(t)
		winner := &Runtime{store: storage}
		loser := &Runtime{store: &staleStopStore{RuntimeStore: storage, complete: func() error {
			_, err := winner.CompleteRun(run.ID)
			return err
		}}}
		stopped, err := loser.CancelRun(run.ID)
		if err != nil {
			t.Fatal(err)
		}
		if stopped.Status != domain.RunCompleted {
			t.Fatalf("stale Stop returned %s, want completed", stopped.Status)
		}
		if _, err := winner.CompleteRun(run.ID); err != nil {
			t.Fatal(err)
		}
		assertLifecycleFact(t, storage, run.ID, domain.RunCompleted, domain.EventRunCompleted)
	})
	t.Run("Stop wins late completion and failure", func(t *testing.T) {
		run := newRun(t)
		runtime := &Runtime{store: storage}
		if _, err := runtime.CancelRun(run.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := runtime.CompleteRun(run.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := runtime.FailRun(run.ID, errors.New("late provider failure")); err != nil {
			t.Fatal(err)
		}
		if _, err := runtime.CancelRun(run.ID); err != nil {
			t.Fatal(err)
		}
		assertLifecycleFact(t, storage, run.ID, domain.RunCanceled, domain.EventRunCanceled)
	})
	t.Run("failure wins late completion", func(t *testing.T) {
		run := newRun(t)
		runtime := &Runtime{store: storage}
		if _, err := runtime.FailRun(run.ID, errors.New("real execution failure")); err != nil {
			t.Fatal(err)
		}
		if _, err := runtime.CompleteRun(run.ID); err != nil {
			t.Fatal(err)
		}
		assertLifecycleFact(t, storage, run.ID, domain.RunFailed, domain.EventRunFailed)
	})
}

func assertLifecycleFact(t *testing.T, storage *store.PostgresStore, id string, status domain.RunStatus, kind domain.RunEventType) {
	t.Helper()
	run, found, err := storage.GetRun(id)
	if err != nil || !found || run.Status != status {
		t.Fatalf("persisted run = %+v found=%v err=%v", run, found, err)
	}
	events, err := storage.ListRunEvents(id)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, item := range events {
		if item.Type == kind {
			count++
		}
	}
	if count != 1 || events[len(events)-1].Type != kind {
		t.Fatalf("expected exactly one committed %s, got %+v", kind, events)
	}
	replay, found, err := storage.GetRunReplay(id)
	if err != nil || !found || replay.Projection.Run.Status != status {
		t.Fatalf("replay disagrees with Run: %+v %v", replay.Projection, err)
	}
	if failures := event.CheckRuntimeInvariants(run, events); len(failures) != 0 {
		t.Fatalf("lifecycle facts violate protocol: %+v", failures)
	}
}
