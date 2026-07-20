package agent

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/store"
)

func TestRunBudgetResumeIgnoresWaitingWallClock(t *testing.T) {
	fileStore, err := store.NewFileStore(filepath.Join(t.TempDir(), "agentflow.json"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	conversation, err := fileStore.CreateConversation("resume budget")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	snapshot := testRuntimeSnapshot()
	snapshot.RunBudget = &domain.RuntimeRunBudget{MaxRuntimeMS: 50}
	run, err := fileStore.CreateRun("agent_planner", conversation.ID, snapshot)
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	if _, err = fileStore.UpdateRunStatus(run.ID, domain.RunRunning, ""); err != nil {
		t.Fatalf("start run: %v", err)
	}
	time.Sleep(5 * time.Millisecond)
	if _, err = fileStore.UpdateRunStatus(run.ID, domain.RunWaitingForUser, ""); err != nil {
		t.Fatalf("pause run: %v", err)
	}
	time.Sleep(70 * time.Millisecond)
	if _, err = fileStore.UpdateRunStatus(run.ID, domain.RunRunning, ""); err != nil {
		t.Fatalf("resume run: %v", err)
	}
	runtime := &Runtime{store: fileStore}
	ctx, cancel, err := runtime.contextWithRunBudget(context.Background(), run.ID)
	defer cancel()
	if err != nil {
		t.Fatalf("waiting wall clock exhausted runtime budget: %v", err)
	}
	select {
	case <-ctx.Done():
		t.Fatalf("resumed budget context was already expired: %v", context.Cause(ctx))
	default:
	}
}
