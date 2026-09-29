package store

import (
	"testing"

	"agentflow-platform/apps/api/internal/domain"
)

func TestPostgresTurnScopedToolEffectRoundTrip(t *testing.T) {
	postgres := openPostgresTestStore(t)
	conversation, err := postgres.CreateConversation("Turn-scoped Tool effect regression")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = postgres.DeleteConversation(conversation.ID) })
	run, err := postgres.CreateRunWithContract("agent_planner", conversation.ID, testRuntimeSnapshot(), nil)
	if err != nil {
		t.Fatal(err)
	}
	input := domain.ToolEffectRecord{IdempotencyKey: "effect-" + run.ID, RunID: run.ID, TurnID: "turn-1", ToolCallID: "call-1", ToolName: "update_task_state", RequestHash: "hash"}
	if _, execute, err := postgres.BeginToolEffect(input); err != nil || !execute {
		t.Fatalf("begin Turn effect: execute=%v err=%v", execute, err)
	}
	if _, err := postgres.CompleteToolEffect(input.IdempotencyKey, []byte(`{"applied":true}`)); err != nil {
		t.Fatal(err)
	}
	if replayed, execute, err := postgres.BeginToolEffect(input); err != nil || execute || replayed.Status != domain.ToolEffectCommitted || replayed.StageID != "" || replayed.TurnID != input.TurnID {
		t.Fatalf("Turn effect round-trip: %#v execute=%v err=%v", replayed, execute, err)
	}
}
