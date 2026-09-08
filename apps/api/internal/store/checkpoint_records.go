package store

import (
	"errors"

	"strings"

	"agentflow-platform/apps/api/internal/domain"
)

func ValidateStageCheckpoint(checkpoint domain.StageCheckpoint) error {
	if strings.TrimSpace(checkpoint.Provider) == "" || strings.TrimSpace(checkpoint.RunID) == "" || strings.TrimSpace(checkpoint.StageID) == "" {
		return errors.New("checkpoint requires provider, run_id, and stage_id")
	}
	if checkpoint.Status == "" || checkpoint.InputHash == "" || checkpoint.RuntimeSnapshotHash == "" || checkpoint.ToolDefinitionsHash == "" {
		return errors.New("checkpoint requires status and state hashes")
	}
	return nil
}

func ValidateCheckpointUpdate(existing, next domain.StageCheckpoint) error {
	if existing.Provider != next.Provider || existing.InputHash != next.InputHash || existing.RuntimeSnapshotHash != next.RuntimeSnapshotHash || existing.ToolDefinitionsHash != next.ToolDefinitionsHash {
		return errors.New("checkpoint immutable state changed")
	}
	if next.EventCursor < existing.EventCursor {
		return errors.New("checkpoint event cursor cannot move backwards")
	}
	if !checkpointTransitionAllowed(existing.Status, next.Status) {
		return errors.New("invalid checkpoint status transition")
	}
	return nil
}

func checkpointTransitionAllowed(current, next domain.StageCheckpointStatus) bool {
	if current == next {
		return true
	}
	switch current {
	case domain.CheckpointPrepared:
		return next == domain.CheckpointExecuting || next == domain.CheckpointNeedsReconciliation
	case domain.CheckpointExecuting:
		return next == domain.CheckpointCommitted || next == domain.CheckpointNeedsReconciliation
	case domain.CheckpointNeedsReconciliation:
		return next == domain.CheckpointCompensated
	default:
		return false
	}
}

func ValidateToolEffect(effect domain.ToolEffectRecord) error {
	if strings.TrimSpace(effect.IdempotencyKey) == "" || strings.TrimSpace(effect.RunID) == "" || strings.TrimSpace(effect.StageID) == "" || strings.TrimSpace(effect.ToolCallID) == "" || strings.TrimSpace(effect.ToolName) == "" || strings.TrimSpace(effect.RequestHash) == "" {
		return errors.New("tool effect requires idempotency, execution identity, and request hash")
	}
	return nil
}

func CloneToolEffect(effect domain.ToolEffectRecord) domain.ToolEffectRecord {
	effect.Version = max(effect.Version, 1)
	effect.Result = append([]byte(nil), effect.Result...)
	return effect
}
