package checkpoint

import (
	"errors"
	"reflect"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
)

// Failure inventory: a later invalid/uncertain checkpoint must not cause earlier
// checkpoints or their evidence to be changed during recovery validation.
func TestRestoreRejectsBeforeMutatingAnyCheckpoint(t *testing.T) {
	storage, run := checkpointTestRun(t)
	provider := NewInternalProvider(storage)
	for _, id := range []string{"first", "uncertain"} {
		_, err := provider.RecordStageTransition(t.Context(), domain.CollaborationStep{ID: id, RunID: run.ID, ConversationID: run.ConversationID, Role: "worker", Input: "synthetic"}, domain.EventStageStarted)
		if err != nil {
			t.Fatal(err)
		}
	}
	_, _, err := storage.BeginToolEffect(domain.ToolEffectRecord{IdempotencyKey: "uncertain-effect", RunID: run.ID, StageID: "uncertain", ToolCallID: "call", ToolName: "write", RequestHash: "synthetic"})
	if err != nil {
		t.Fatal(err)
	}
	before, err := storage.ListStageCheckpoints(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	events, err := storage.ListRunEvents(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.RestoreRun(t.Context(), run); !errors.Is(err, ErrNeedsReconciliation) {
		t.Fatalf("expected uncertain rejection: %v", err)
	}
	after, err := storage.ListStageCheckpoints(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	later, err := storage.ListRunEvents(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(events, later) {
		t.Fatal("rejected restore changed checkpoints or events")
	}
}
