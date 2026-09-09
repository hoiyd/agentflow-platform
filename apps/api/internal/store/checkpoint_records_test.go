package store

import (
	"testing"

	"agentflow-platform/apps/api/internal/domain"
)

// These are shared persistence invariants, not File Store lifecycle tests.
func TestCheckpointValidationAndTransitionContract(t *testing.T) {
	base := domain.StageCheckpoint{Provider: "internal_state_v1", RunID: "run", StageID: "stage", Status: domain.CheckpointPrepared, InputHash: "input", RuntimeSnapshotHash: "snapshot", ToolDefinitionsHash: "tools", EventCursor: 1}
	if err := ValidateStageCheckpoint(base); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*domain.StageCheckpoint){
		func(c *domain.StageCheckpoint) { c.Provider = "" },
		func(c *domain.StageCheckpoint) { c.InputHash = "" },
	} {
		invalid := base
		change(&invalid)
		if err := ValidateStageCheckpoint(invalid); err == nil {
			t.Fatal("invalid checkpoint accepted")
		}
	}
	states := []domain.StageCheckpointStatus{domain.CheckpointPrepared, domain.CheckpointExecuting, domain.CheckpointCommitted, domain.CheckpointNeedsReconciliation, domain.CheckpointCompensated}
	for _, from := range states {
		for _, to := range states {
			old, next := base, base
			old.Status, next.Status = from, to
			allowed := from == to ||
				from == domain.CheckpointPrepared && (to == domain.CheckpointExecuting || to == domain.CheckpointNeedsReconciliation) ||
				from == domain.CheckpointExecuting && (to == domain.CheckpointCommitted || to == domain.CheckpointNeedsReconciliation) ||
				from == domain.CheckpointNeedsReconciliation && to == domain.CheckpointCompensated
			if err := ValidateCheckpointUpdate(old, next); (err == nil) != allowed {
				t.Fatalf("%s -> %s: allowed=%t err=%v", from, to, allowed, err)
			}
		}
	}
	for _, change := range []func(*domain.StageCheckpoint){
		func(c *domain.StageCheckpoint) { c.Provider = "other" },
		func(c *domain.StageCheckpoint) { c.InputHash = "other" },
		func(c *domain.StageCheckpoint) { c.RuntimeSnapshotHash = "other" },
		func(c *domain.StageCheckpoint) { c.ToolDefinitionsHash = "other" },
		func(c *domain.StageCheckpoint) { c.EventCursor = 0 },
	} {
		next := base
		change(&next)
		if err := ValidateCheckpointUpdate(base, next); err == nil {
			t.Fatal("unsafe checkpoint update accepted")
		}
	}
}
