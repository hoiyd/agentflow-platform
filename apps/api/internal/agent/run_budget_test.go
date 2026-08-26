package agent

import (
	"context"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/store"
)

func TestRunBudgetResumeIgnoresWaitingWallClock(t *testing.T) {
	now := time.Now().UTC()
	createdAt := now.Add(-time.Hour)
	resumedAt := now.Add(-10 * time.Millisecond)
	snapshot := testRuntimeSnapshot()
	snapshot.RunBudget = &domain.RuntimeRunBudget{MaxRuntimeMS: 30_000}
	run := domain.Run{
		ID: "run-1", Status: domain.RunRunning, RuntimeSnapshot: &snapshot,
		StartedAt: &createdAt, ExecutionStartedAt: &resumedAt, ActiveRuntimeMS: 25,
	}
	runtime := &Runtime{store: &fixedRunBudgetStore{run: run}}
	ctx, cancel, err := runtime.contextWithRunBudget(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("waiting wall clock exhausted runtime budget: %v", err)
	}
	defer cancel()
	select {
	case <-ctx.Done():
		t.Fatalf("resumed budget context was already expired: %v", context.Cause(ctx))
	default:
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("runtime budget did not set a deadline")
	}
	remaining := time.Until(deadline)
	if remaining < 20*time.Second || remaining > 30*time.Second {
		t.Fatalf("remaining runtime = %s; waiting wall clock appears to have been counted", remaining)
	}
}

type fixedRunBudgetStore struct {
	store.Store
	run domain.Run
}

func (s *fixedRunBudgetStore) GetRun(id string) (domain.Run, bool, error) {
	if id != s.run.ID {
		return domain.Run{}, false, nil
	}
	return s.run, true, nil
}
