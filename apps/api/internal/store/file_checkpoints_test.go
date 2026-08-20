package store

import (
	"encoding/json"
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
	run, err := fileStore.CreateRun("agent_planner", conversation.ID, testRuntimeSnapshot())
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
