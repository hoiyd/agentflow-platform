package checkpoint

import (
	"context"
	"errors"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/store"
)

func TestInternalProviderCapturesAndRestoresCommittedStage(t *testing.T) {
	fileStore, run := checkpointTestRun(t)
	provider := NewInternalProvider(fileStore)
	step := domain.CollaborationStep{
		ID: "stage-1", RunID: run.ID, ConversationID: run.ConversationID,
		Role: "worker", Status: domain.CollaborationStepRunning, Input: "do work",
	}
	if _, err := provider.RecordStageTransition(context.Background(), step, domain.EventStageStarted); err != nil {
		t.Fatalf("start stage: %v", err)
	}
	step.Status = domain.CollaborationStepCompleted
	step.Output = "done"
	if _, err := provider.RecordStageTransition(context.Background(), step, domain.EventStageCompleted); err != nil {
		t.Fatalf("complete stage: %v", err)
	}
	checkpoint, ok, err := fileStore.GetStageCheckpoint(run.ID, step.ID)
	if err != nil || !ok || checkpoint.Status != domain.CheckpointCommitted || checkpoint.EventCursor != 3 {
		t.Fatalf("unexpected checkpoint: %#v ok=%v err=%v", checkpoint, ok, err)
	}
	report, err := provider.RestoreRun(context.Background(), run)
	if err != nil {
		t.Fatalf("restore run: %v", err)
	}
	if len(report.CommittedStageIDs) != 1 || report.CommittedStageIDs[0] != step.ID {
		t.Fatalf("unexpected restore report: %#v", report)
	}
	events, err := fileStore.ListRunEvents(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := event.ValidateLifecycle(events); err != nil {
		t.Fatalf("checkpoint events broke lifecycle: %v", err)
	}
}

func TestInternalProviderRejectsStaleRuntimeSnapshot(t *testing.T) {
	fileStore, run := checkpointTestRun(t)
	provider := NewInternalProvider(fileStore)
	step := domain.CollaborationStep{ID: "stage-1", RunID: run.ID, ConversationID: run.ConversationID, Role: "worker", Input: "work"}
	if _, err := provider.RecordStageTransition(context.Background(), step, domain.EventStageCompleted); err != nil {
		t.Fatalf("capture stage: %v", err)
	}
	changed := run
	copySnapshot := *run.RuntimeSnapshot
	copySnapshot.Model.Model = "different-model"
	changed.RuntimeSnapshot = &copySnapshot
	if _, err := provider.RestoreRun(context.Background(), changed); !errors.Is(err, ErrCheckpointStale) {
		t.Fatalf("expected stale checkpoint error, got %v", err)
	}
}

func TestInternalProviderCommitsDurableStageTerminalAfterInterruptedCheckpointWrite(t *testing.T) {
	fileStore, run := checkpointTestRun(t)
	provider := NewInternalProvider(fileStore)
	step := domain.CollaborationStep{ID: "stage-1", RunID: run.ID, ConversationID: run.ConversationID, Role: "worker", Input: "work"}
	if _, err := provider.RecordStageTransition(context.Background(), step, domain.EventStageStarted); err != nil {
		t.Fatalf("start stage: %v", err)
	}
	if _, err := fileStore.CreateRunEvent(domain.RunEvent{
		Type: domain.EventStageCompleted, RunID: run.ID, ConversationID: run.ConversationID,
		StageID: step.ID, Payload: map[string]any{"output": "durable output"},
	}); err != nil {
		t.Fatalf("persist terminal event: %v", err)
	}
	report, err := provider.RestoreRun(context.Background(), run)
	if err != nil {
		t.Fatalf("restore interrupted checkpoint: %v", err)
	}
	checkpoint, ok, err := fileStore.GetStageCheckpoint(run.ID, step.ID)
	if err != nil || !ok || checkpoint.Status != domain.CheckpointCommitted || checkpoint.OutputHash != hashValue("durable output") {
		t.Fatalf("terminal reconciliation failed: %#v ok=%v err=%v report=%#v", checkpoint, ok, err, report)
	}
}

func TestInternalProviderAdoptsLegacyOpenStageWithoutDuplicatingStart(t *testing.T) {
	fileStore, run := checkpointTestRun(t)
	provider := NewInternalProvider(fileStore)
	step := domain.CollaborationStep{ID: "stage-1", RunID: run.ID, ConversationID: run.ConversationID, Role: "human_input", Input: "question", Output: "answer"}
	if _, err := fileStore.CreateRunEvent(domain.RunEvent{
		Type: domain.EventStageStarted, RunID: run.ID, ConversationID: run.ConversationID, StageID: step.ID,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.RecordStageTransition(context.Background(), step, domain.EventStageCompleted); err != nil {
		t.Fatalf("complete legacy stage: %v", err)
	}
	events, err := fileStore.ListRunEvents(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	starts := 0
	for _, item := range events {
		if item.Type == domain.EventStageStarted {
			starts++
		}
	}
	if starts != 1 {
		t.Fatalf("expected one stage start, got %d events=%#v", starts, events)
	}
	if err := event.ValidateLifecycle(events); err != nil {
		t.Fatalf("adopted legacy lifecycle: %v", err)
	}
}

func TestInternalProviderFailsClosedForUncertainToolEffect(t *testing.T) {
	fileStore, run := checkpointTestRun(t)
	provider := NewInternalProvider(fileStore)
	step := domain.CollaborationStep{ID: "stage-1", RunID: run.ID, ConversationID: run.ConversationID, Role: "worker", Input: "write"}
	if _, err := provider.RecordStageTransition(context.Background(), step, domain.EventStageStarted); err != nil {
		t.Fatalf("start stage: %v", err)
	}
	if _, _, err := fileStore.BeginToolEffect(domain.ToolEffectRecord{
		IdempotencyKey: "effect-1", RunID: run.ID, StageID: step.ID, TurnID: "turn-1",
		ToolCallID: "call-1", ToolName: "write_record", RequestHash: "sha256:request",
	}); err != nil {
		t.Fatalf("begin tool effect: %v", err)
	}
	if _, err := provider.RestoreRun(context.Background(), run); !errors.Is(err, ErrNeedsReconciliation) {
		t.Fatalf("expected reconciliation error, got %v", err)
	}
}

func checkpointTestRun(t *testing.T) (*store.FileStore, domain.Run) {
	t.Helper()
	fileStore, err := store.NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	conversation, err := fileStore.CreateConversation("Checkpoint test")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	run, err := fileStore.CreateRun("agent_planner", conversation.ID, domain.RuntimeSnapshot{
		SchemaVersion:    domain.CurrentRuntimeSnapshotVersion,
		Mode:             "autonomous",
		Agent:            domain.RuntimeAgentSnapshot{ID: "agent_planner"},
		Model:            domain.RuntimeModelSnapshot{Provider: "test", Model: "test-model"},
		Tools:            []domain.RuntimeToolSnapshot{},
		AutonomousLimits: &domain.RuntimeLimitsSnapshot{MaxIterations: 2},
		RunBudget:        &domain.RuntimeRunBudget{},
	})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	return fileStore, run
}
