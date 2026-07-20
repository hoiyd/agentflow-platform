package recovery

import (
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/store"
)

func TestMarkStaleRunningRuns(t *testing.T) {
	fileStore, err := store.NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	conversation, err := fileStore.CreateConversation("Recovery scan")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	run, err := fileStore.CreateRun("agent_planner", conversation.ID, domain.RuntimeSnapshot{
		SchemaVersion: domain.CurrentRuntimeSnapshotVersion, RunBudget: &domain.RuntimeRunBudget{},
	})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	if _, err := fileStore.UpdateRunStatus(run.ID, domain.RunRunning, ""); err != nil {
		t.Fatalf("mark running: %v", err)
	}
	time.Sleep(time.Millisecond)

	count, err := MarkStaleRunningRuns(fileStore, time.Nanosecond)
	if err != nil {
		t.Fatalf("mark stale running runs: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected one recovered run, got %d", count)
	}
	updated, ok, err := fileStore.GetRun(run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if !ok || updated.Status != domain.RunFailedRecoverable {
		t.Fatalf("expected failed_recoverable run, got %#v", updated)
	}
	if updated.Error != staleRunMessage {
		t.Fatalf("expected stale message, got %q", updated.Error)
	}
}
