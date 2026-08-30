package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
)

func TestFileStoreCheckpointAndToolEffectRoundTrip(t *testing.T) {
	path := t.TempDir() + "/agentflow.json"
	fileStore, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := fileStore.CreateConversation("Durable recovery")
	if err != nil {
		t.Fatal(err)
	}
	run, err := fileStore.CreateRunWithContract("agent_planner", conversation.ID, testRuntimeSnapshot(), nil)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, err := fileStore.SaveStageCheckpoint(domain.StageCheckpoint{
		Provider: "internal_state_v1", RunID: run.ID, ConversationID: conversation.ID,
		StageID: "stage-1", Status: domain.CheckpointPrepared, InputHash: "input",
		RuntimeSnapshotHash: "snapshot", ToolDefinitionsHash: "tools",
	})
	if err != nil {
		t.Fatal(err)
	}
	checkpoint.Status = domain.CheckpointExecuting
	checkpoint.EventCursor = 1
	if _, err := fileStore.SaveStageCheckpoint(checkpoint); err != nil {
		t.Fatal(err)
	}
	effect, execute, err := fileStore.BeginToolEffect(domain.ToolEffectRecord{
		IdempotencyKey: "effect-1", RunID: run.ID, StageID: "stage-1", TurnID: "turn-1",
		ToolCallID: "call-1", ToolName: "write_record", RequestHash: "request",
	})
	if err != nil || !execute {
		t.Fatalf("begin effect: execute=%v effect=%#v err=%v", execute, effect, err)
	}
	if _, err := fileStore.CompleteToolEffect(effect.IdempotencyKey, []byte(`{"ok":true}`)); err != nil {
		t.Fatal(err)
	}

	reopened, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	checkpoints, err := reopened.ListStageCheckpoints(run.ID)
	if err != nil || len(checkpoints) != 1 || checkpoints[0].Status != domain.CheckpointExecuting {
		t.Fatalf("checkpoint round trip: %#v err=%v", checkpoints, err)
	}
	effects, err := reopened.ListToolEffects(run.ID)
	if err != nil || len(effects) != 1 || effects[0].Status != domain.ToolEffectCommitted || string(effects[0].Result) != `{"ok":true}` {
		t.Fatalf("effect round trip: %#v err=%v", effects, err)
	}
	replay, ok, err := reopened.GetRunReplay(run.ID)
	if err != nil || !ok || len(replay.ToolEffects) != 1 || !replay.ToolEffects[0].HasResult {
		t.Fatalf("replay effect summary: %#v ok=%v err=%v", replay.ToolEffects, ok, err)
	}
	encoded, err := json.Marshal(replay)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), `"result":`) {
		t.Fatalf("replay leaked durable tool result: %s", encoded)
	}
}

func TestFileStoreCheckpointStateMachineRejectsUnsafeUpdates(t *testing.T) {
	fileStore, run := checkpointFileTestRun(t)
	if _, err := fileStore.SaveStageCheckpoint(domain.StageCheckpoint{}); err == nil {
		t.Fatal("expected invalid checkpoint error")
	}
	prepared := domain.StageCheckpoint{
		Provider: "internal_state_v1", RunID: run.ID, ConversationID: run.ConversationID,
		StageID: "stage-1", Status: domain.CheckpointPrepared, InputHash: "input",
		RuntimeSnapshotHash: "snapshot", ToolDefinitionsHash: "tools", EventCursor: 1,
	}
	stored, err := fileStore.SaveStageCheckpoint(prepared)
	if err != nil {
		t.Fatal(err)
	}
	if found, ok, err := fileStore.GetStageCheckpoint(run.ID, stored.StageID); err != nil || !ok || found.ID != stored.ID {
		t.Fatalf("get checkpoint: %#v ok=%v err=%v", found, ok, err)
	}
	if _, ok, err := fileStore.GetStageCheckpoint(run.ID, "missing"); err != nil || ok {
		t.Fatalf("missing checkpoint: ok=%v err=%v", ok, err)
	}

	changed := stored
	changed.InputHash = "different"
	if _, err := fileStore.SaveStageCheckpoint(changed); err == nil {
		t.Fatal("expected immutable checkpoint error")
	}
	invalidTransition := stored
	invalidTransition.Status = domain.CheckpointCommitted
	if _, err := fileStore.SaveStageCheckpoint(invalidTransition); err == nil {
		t.Fatal("expected prepared-to-committed transition error")
	}
	executing := stored
	executing.Status = domain.CheckpointExecuting
	executing.EventCursor = 2
	executing, err = fileStore.SaveStageCheckpoint(executing)
	if err != nil {
		t.Fatal(err)
	}
	backwards := executing
	backwards.EventCursor = 1
	if _, err := fileStore.SaveStageCheckpoint(backwards); err == nil {
		t.Fatal("expected backwards cursor error")
	}
	needsReconciliation := executing
	needsReconciliation.Status = domain.CheckpointNeedsReconciliation
	needsReconciliation, err = fileStore.SaveStageCheckpoint(needsReconciliation)
	if err != nil {
		t.Fatal(err)
	}
	compensated := needsReconciliation
	compensated.Status = domain.CheckpointCompensated
	compensated, err = fileStore.SaveStageCheckpoint(compensated)
	if err != nil {
		t.Fatal(err)
	}
	compensated.Status = domain.CheckpointExecuting
	if _, err := fileStore.SaveStageCheckpoint(compensated); err == nil {
		t.Fatal("expected terminal checkpoint transition error")
	}
}

func TestFileStoreToolEffectJournalIsIdempotentAndFailClosed(t *testing.T) {
	fileStore, run := checkpointFileTestRun(t)
	if _, _, err := fileStore.BeginToolEffect(domain.ToolEffectRecord{}); err == nil {
		t.Fatal("expected invalid tool effect error")
	}
	request := domain.ToolEffectRecord{
		IdempotencyKey: "effect-1", RunID: run.ID, StageID: "stage-1", TurnID: "turn-1",
		ToolCallID: "call-1", ToolName: "writer", RequestHash: "request-1",
	}
	effect, execute, err := fileStore.BeginToolEffect(request)
	if err != nil || !execute || effect.Status != domain.ToolEffectExecuting {
		t.Fatalf("begin effect: %#v execute=%v err=%v", effect, execute, err)
	}
	if duplicate, execute, err := fileStore.BeginToolEffect(request); err != nil || execute || duplicate.Status != domain.ToolEffectExecuting {
		t.Fatalf("duplicate effect: %#v execute=%v err=%v", duplicate, execute, err)
	}
	conflict := request
	conflict.RequestHash = "different"
	if _, _, err := fileStore.BeginToolEffect(conflict); err == nil {
		t.Fatal("expected idempotency-key conflict")
	}
	committed, err := fileStore.CompleteToolEffect(request.IdempotencyKey, []byte(`{"ok":true}`))
	if err != nil || committed.Status != domain.ToolEffectCommitted {
		t.Fatalf("commit effect: %#v err=%v", committed, err)
	}
	if _, err := fileStore.CompleteToolEffect(request.IdempotencyKey, []byte(`{"ok":true}`)); err != nil {
		t.Fatalf("idempotent commit: %v", err)
	}
	if _, err := fileStore.CompleteToolEffect(request.IdempotencyKey, []byte(`{"ok":false}`)); err == nil {
		t.Fatal("expected committed result conflict")
	}
	if _, err := fileStore.MarkToolEffectNeedsReconciliation(request.IdempotencyKey, "late failure"); err == nil {
		t.Fatal("expected terminal effect reconciliation error")
	}
	if _, err := fileStore.CompleteToolEffect("missing", nil); err == nil {
		t.Fatal("expected missing effect error")
	}
	if _, err := fileStore.MarkToolEffectNeedsReconciliation("missing", "failure"); err == nil {
		t.Fatal("expected missing reconciliation error")
	}

	second := request
	second.IdempotencyKey = "effect-2"
	second.ToolCallID = "call-2"
	second.RequestHash = "request-2"
	if _, _, err := fileStore.BeginToolEffect(second); err != nil {
		t.Fatal(err)
	}
	uncertain, err := fileStore.MarkToolEffectNeedsReconciliation(second.IdempotencyKey, "timeout")
	if err != nil || uncertain.Status != domain.ToolEffectNeedsReconciliation || uncertain.Error != "timeout" {
		t.Fatalf("mark reconciliation: %#v err=%v", uncertain, err)
	}
	if _, err := fileStore.CompleteToolEffect(second.IdempotencyKey, nil); err == nil {
		t.Fatal("expected non-executing completion error")
	}
	effects, err := fileStore.ListToolEffects(run.ID)
	if err != nil || len(effects) != 2 {
		t.Fatalf("list effects: %#v err=%v", effects, err)
	}
}

func TestFileStoreCheckpointWritesRollbackInMemoryOnPersistenceFailure(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "agentflow.json")
	fileStore, run := checkpointFileTestRunAtPath(t, path)
	checkpoint, err := fileStore.SaveStageCheckpoint(domain.StageCheckpoint{
		Provider: "internal_state_v1", RunID: run.ID, ConversationID: run.ConversationID,
		StageID: "stage-1", Status: domain.CheckpointPrepared, InputHash: "input",
		RuntimeSnapshotHash: "snapshot", ToolDefinitionsHash: "tools",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(directory); err != nil {
		t.Fatal(err)
	}
	checkpoint.Status = domain.CheckpointExecuting
	if _, err := fileStore.SaveStageCheckpoint(checkpoint); err == nil {
		t.Fatal("expected checkpoint persistence error")
	}
	stored, ok, err := fileStore.GetStageCheckpoint(run.ID, checkpoint.StageID)
	if err != nil || !ok || stored.Status != domain.CheckpointPrepared {
		t.Fatalf("checkpoint rollback failed: %#v ok=%v err=%v", stored, ok, err)
	}
	if _, _, err := fileStore.BeginToolEffect(domain.ToolEffectRecord{
		IdempotencyKey: "effect-1", RunID: run.ID, StageID: checkpoint.StageID,
		ToolCallID: "call-1", ToolName: "writer", RequestHash: "request",
	}); err == nil {
		t.Fatal("expected effect persistence error")
	}
	effects, err := fileStore.ListToolEffects(run.ID)
	if err != nil || len(effects) != 0 {
		t.Fatalf("effect rollback failed: %#v err=%v", effects, err)
	}
}

func checkpointFileTestRun(t *testing.T) (*FileStore, domain.Run) {
	t.Helper()
	return checkpointFileTestRunAtPath(t, filepath.Join(t.TempDir(), "agentflow.json"))
}

func checkpointFileTestRunAtPath(t *testing.T, path string) (*FileStore, domain.Run) {
	t.Helper()
	fileStore, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := fileStore.CreateConversation("Checkpoint state machine")
	if err != nil {
		t.Fatal(err)
	}
	run, err := fileStore.CreateRunWithContract("agent_planner", conversation.ID, testRuntimeSnapshot(), nil)
	if err != nil {
		t.Fatal(err)
	}
	return fileStore, run
}
