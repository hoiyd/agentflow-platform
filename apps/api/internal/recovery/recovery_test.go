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
	step, err := fileStore.CreateCollaborationStep(domain.CollaborationStep{
		RunID: run.ID, ConversationID: conversation.ID, Role: "worker",
		Status: domain.CollaborationStepRunning, Input: "continue work",
	})
	if err != nil {
		t.Fatalf("create running step: %v", err)
	}
	for _, item := range []domain.RunEvent{
		{Type: domain.EventStageStarted, RunID: run.ID, ConversationID: conversation.ID, StageID: "stage-1"},
		{Type: domain.EventTurnStarted, RunID: run.ID, ConversationID: conversation.ID, StageID: "stage-1", TurnID: "turn-1"},
		{Type: domain.EventModelStarted, RunID: run.ID, ConversationID: conversation.ID, StageID: "stage-1", TurnID: "turn-1"},
	} {
		if _, err := fileStore.CreateRunEvent(item); err != nil {
			t.Fatalf("create run event: %v", err)
		}
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
	events, err := fileStore.ListRunEvents(run.ID)
	if err != nil {
		t.Fatalf("list repaired events: %v", err)
	}
	if len(events) != 6 {
		t.Fatalf("expected three synthetic terminals, got %d events", len(events))
	}
	for _, item := range events[3:] {
		if item.Payload["synthetic"] != true {
			t.Fatalf("expected synthetic terminal event, got %#v", item)
		}
	}
	steps, err := fileStore.ListCollaborationSteps(run.ID)
	if err != nil || len(steps) != 1 || steps[0].ID != step.ID || steps[0].Status != domain.CollaborationStepFailed {
		t.Fatalf("expected interrupted stage record to fail: %#v err=%v", steps, err)
	}

	count, err = MarkStaleRunningRuns(fileStore, time.Nanosecond)
	if err != nil || count != 0 {
		t.Fatalf("expected idempotent second scan, count=%d err=%v", count, err)
	}
}
