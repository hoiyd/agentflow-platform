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

func TestInternalProviderCompensatesInterruptedInternalStage(t *testing.T) {
	fileStore, run := checkpointTestRun(t)
	provider := NewInternalProvider(fileStore)
	step := domain.CollaborationStep{ID: "stage-1", RunID: run.ID, ConversationID: run.ConversationID, Role: "worker", Input: "work"}
	if _, err := provider.RecordStageTransition(context.Background(), step, domain.EventStageStarted); err != nil {
		t.Fatalf("start stage: %v", err)
	}

	report, err := provider.RestoreRun(context.Background(), run)
	if err != nil {
		t.Fatalf("restore interrupted stage: %v", err)
	}
	if len(report.CompensatedStageIDs) != 1 || report.CompensatedStageIDs[0] != step.ID {
		t.Fatalf("unexpected compensation report: %#v", report)
	}
	checkpoint, ok, err := fileStore.GetStageCheckpoint(run.ID, step.ID)
	if err != nil || !ok || checkpoint.Status != domain.CheckpointCompensated {
		t.Fatalf("expected compensated checkpoint: %#v ok=%v err=%v", checkpoint, ok, err)
	}
	restoredAgain, err := provider.RestoreRun(context.Background(), run)
	if err != nil || len(restoredAgain.CompensatedStageIDs) != 1 {
		t.Fatalf("restore compensated checkpoint: %#v err=%v", restoredAgain, err)
	}
}

func TestInternalProviderReconcilesFailedTerminalBeforeCompensation(t *testing.T) {
	fileStore, run := checkpointTestRun(t)
	provider := NewInternalProvider(fileStore)
	step := domain.CollaborationStep{ID: "stage-1", RunID: run.ID, ConversationID: run.ConversationID, Role: "worker", Input: "work"}
	if _, err := provider.RecordStageTransition(context.Background(), step, domain.EventStageStarted); err != nil {
		t.Fatal(err)
	}
	if _, err := fileStore.CreateRunEvent(domain.RunEvent{
		Type: domain.EventStageFailed, RunID: run.ID, ConversationID: run.ConversationID,
		StageID: step.ID, Payload: map[string]any{"error": "worker interrupted"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.RestoreRun(context.Background(), run); err != nil {
		t.Fatalf("restore failed terminal: %v", err)
	}
	checkpoint, ok, err := fileStore.GetStageCheckpoint(run.ID, step.ID)
	if err != nil || !ok || checkpoint.Status != domain.CheckpointCompensated || checkpoint.Error != "worker interrupted" {
		t.Fatalf("unexpected reconciled checkpoint: %#v ok=%v err=%v", checkpoint, ok, err)
	}
}

func TestInternalProviderRecordsFailedAndCanceledTransitions(t *testing.T) {
	for _, terminal := range []domain.RunEventType{domain.EventStageFailed, domain.EventStageCanceled} {
		t.Run(string(terminal), func(t *testing.T) {
			fileStore, run := checkpointTestRun(t)
			provider := NewInternalProvider(fileStore)
			step := domain.CollaborationStep{ID: "stage-1", RunID: run.ID, ConversationID: run.ConversationID, Role: "worker", Input: "work"}
			if _, err := provider.RecordStageTransition(context.Background(), step, domain.EventStageStarted); err != nil {
				t.Fatal(err)
			}
			step.Error = "stopped"
			if _, err := provider.RecordStageTransition(context.Background(), step, terminal); err != nil {
				t.Fatalf("record %s: %v", terminal, err)
			}
			checkpoint, ok, err := fileStore.GetStageCheckpoint(run.ID, step.ID)
			if err != nil || !ok || checkpoint.Status != domain.CheckpointNeedsReconciliation || checkpoint.Error != "stopped" {
				t.Fatalf("unexpected terminal checkpoint: %#v ok=%v err=%v", checkpoint, ok, err)
			}
		})
	}
}

func TestInternalProviderRejectsInvalidCallsBeforeMutation(t *testing.T) {
	var nilProvider *InternalProvider
	if _, err := nilProvider.RecordStageTransition(context.Background(), domain.CollaborationStep{}, domain.EventStageStarted); err == nil {
		t.Fatal("expected unavailable provider error")
	}
	if _, err := nilProvider.RestoreRun(context.Background(), domain.Run{}); err == nil {
		t.Fatal("expected unavailable provider restore error")
	}
	if _, _, err := RuntimeHashes(nil); err == nil {
		t.Fatal("expected nil snapshot error")
	}

	fileStore, run := checkpointTestRun(t)
	provider := NewInternalProvider(fileStore)
	step := domain.CollaborationStep{ID: "stage-1", RunID: run.ID, ConversationID: run.ConversationID, Role: "worker", Input: "work"}
	if _, err := provider.RecordStageTransition(context.Background(), step, domain.EventRunProgress); err == nil {
		t.Fatal("expected unsupported transition error")
	}
	checkpoints, err := fileStore.ListStageCheckpoints(run.ID)
	if err != nil || len(checkpoints) != 0 {
		t.Fatalf("unsupported event mutated checkpoints: %#v err=%v", checkpoints, err)
	}
	if _, err := provider.RecordStageTransition(context.Background(), step, domain.EventStageStarted); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.RecordStageTransition(context.Background(), step, domain.EventStageStarted); err == nil {
		t.Fatal("expected duplicate stage start error")
	}
}

func TestInternalProviderRejectsUnknownCheckpointStatus(t *testing.T) {
	fileStore, run := checkpointTestRun(t)
	snapshotHash, toolHash, err := RuntimeHashes(run.RuntimeSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fileStore.SaveStageCheckpoint(domain.StageCheckpoint{
		Provider: InternalStateProvider, RunID: run.ID, ConversationID: run.ConversationID,
		StageID: "stage-1", Status: domain.StageCheckpointStatus("unknown"), InputHash: hashValue("work"),
		RuntimeSnapshotHash: snapshotHash, ToolDefinitionsHash: toolHash,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := NewInternalProvider(fileStore).RestoreRun(context.Background(), run); err == nil {
		t.Fatal("expected unknown checkpoint status error")
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
	run, err := fileStore.CreateRunWithContract("agent_planner", conversation.ID, domain.RuntimeSnapshot{
		SchemaVersion:    domain.CurrentRuntimeSnapshotVersion,
		Mode:             "autonomous",
		Agent:            domain.RuntimeAgentSnapshot{ID: "agent_planner"},
		Model:            domain.RuntimeModelSnapshot{Provider: "test", Model: "test-model"},
		Tools:            []domain.RuntimeToolSnapshot{},
		AutonomousLimits: &domain.RuntimeLimitsSnapshot{MaxIterations: 2},
		RunBudget:        &domain.RuntimeRunBudget{},
	}, nil)

	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	return fileStore, run
}
