package store

import (
	"bytes"
	"errors"
	"sort"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/domain"
)

func (s *FileStore) SaveStageCheckpoint(checkpoint domain.StageCheckpoint) (domain.StageCheckpoint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := validateStageCheckpoint(checkpoint); err != nil {
		return domain.StageCheckpoint{}, err
	}
	now := time.Now().UTC()
	for index := range s.data.StageCheckpoints {
		existing := s.data.StageCheckpoints[index]
		if existing.RunID != checkpoint.RunID || existing.StageID != checkpoint.StageID {
			continue
		}
		if err := validateCheckpointUpdate(existing, checkpoint); err != nil {
			return domain.StageCheckpoint{}, err
		}
		checkpoint.ID = existing.ID
		checkpoint.CreatedAt = existing.CreatedAt
		checkpoint.UpdatedAt = now
		s.data.StageCheckpoints[index] = checkpoint
		if err := s.saveLocked(); err != nil {
			s.data.StageCheckpoints[index] = existing
			return domain.StageCheckpoint{}, err
		}
		return checkpoint, nil
	}
	if checkpoint.ID == "" {
		checkpoint.ID = newID("checkpoint")
	}
	checkpoint.CreatedAt = now
	checkpoint.UpdatedAt = now
	s.data.StageCheckpoints = append(s.data.StageCheckpoints, checkpoint)
	if err := s.saveLocked(); err != nil {
		s.data.StageCheckpoints = s.data.StageCheckpoints[:len(s.data.StageCheckpoints)-1]
		return domain.StageCheckpoint{}, err
	}
	return checkpoint, nil
}

func (s *FileStore) GetStageCheckpoint(runID string, stageID string) (domain.StageCheckpoint, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, item := range s.data.StageCheckpoints {
		if item.RunID == runID && item.StageID == stageID {
			return item, true, nil
		}
	}
	return domain.StageCheckpoint{}, false, nil
}

func (s *FileStore) ListStageCheckpoints(runID string) ([]domain.StageCheckpoint, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]domain.StageCheckpoint, 0)
	for _, item := range s.data.StageCheckpoints {
		if item.RunID == runID {
			items = append(items, item)
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].EventCursor == items[j].EventCursor {
			return items[i].StageID < items[j].StageID
		}
		return items[i].EventCursor < items[j].EventCursor
	})
	return items, nil
}

func (s *FileStore) BeginToolEffect(effect domain.ToolEffectRecord) (domain.ToolEffectRecord, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := validateToolEffect(effect); err != nil {
		return domain.ToolEffectRecord{}, false, err
	}
	for _, existing := range s.data.ToolEffects {
		if existing.IdempotencyKey != effect.IdempotencyKey {
			continue
		}
		if existing.RequestHash != effect.RequestHash || existing.ToolName != effect.ToolName || existing.RunID != effect.RunID {
			return domain.ToolEffectRecord{}, false, errors.New("idempotency key was already used for a different tool request")
		}
		return cloneToolEffect(existing), false, nil
	}
	now := time.Now().UTC()
	effect.Status = domain.ToolEffectExecuting
	effect.CreatedAt = now
	effect.UpdatedAt = now
	s.data.ToolEffects = append(s.data.ToolEffects, cloneToolEffect(effect))
	if err := s.saveLocked(); err != nil {
		s.data.ToolEffects = s.data.ToolEffects[:len(s.data.ToolEffects)-1]
		return domain.ToolEffectRecord{}, false, err
	}
	return cloneToolEffect(effect), true, nil
}

func (s *FileStore) CompleteToolEffect(idempotencyKey string, result []byte) (domain.ToolEffectRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for index := range s.data.ToolEffects {
		item := &s.data.ToolEffects[index]
		if item.IdempotencyKey != idempotencyKey {
			continue
		}
		if item.Status == domain.ToolEffectCommitted {
			if !bytes.Equal(item.Result, result) {
				return domain.ToolEffectRecord{}, errors.New("committed tool effect result differs")
			}
			return cloneToolEffect(*item), nil
		}
		if item.Status != domain.ToolEffectExecuting {
			return domain.ToolEffectRecord{}, errors.New("tool effect is not executing")
		}
		previous := cloneToolEffect(*item)
		item.Status = domain.ToolEffectCommitted
		item.Result = append([]byte(nil), result...)
		item.Error = ""
		item.UpdatedAt = time.Now().UTC()
		stored := cloneToolEffect(*item)
		if err := s.saveLocked(); err != nil {
			s.data.ToolEffects[index] = previous
			return domain.ToolEffectRecord{}, err
		}
		return stored, nil
	}
	return domain.ToolEffectRecord{}, ErrNotFound("tool effect")
}

func (s *FileStore) MarkToolEffectNeedsReconciliation(idempotencyKey string, errorMessage string) (domain.ToolEffectRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for index := range s.data.ToolEffects {
		item := &s.data.ToolEffects[index]
		if item.IdempotencyKey != idempotencyKey {
			continue
		}
		if item.Status == domain.ToolEffectCommitted || item.Status == domain.ToolEffectCompensated {
			return domain.ToolEffectRecord{}, errors.New("terminal tool effect cannot require reconciliation")
		}
		previous := cloneToolEffect(*item)
		item.Status = domain.ToolEffectNeedsReconciliation
		item.Error = strings.TrimSpace(errorMessage)
		item.UpdatedAt = time.Now().UTC()
		stored := cloneToolEffect(*item)
		if err := s.saveLocked(); err != nil {
			s.data.ToolEffects[index] = previous
			return domain.ToolEffectRecord{}, err
		}
		return stored, nil
	}
	return domain.ToolEffectRecord{}, ErrNotFound("tool effect")
}

func (s *FileStore) ListToolEffects(runID string) ([]domain.ToolEffectRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]domain.ToolEffectRecord, 0)
	for _, item := range s.data.ToolEffects {
		if item.RunID == runID {
			items = append(items, cloneToolEffect(item))
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.Before(items[j].CreatedAt) })
	return items, nil
}

func validateStageCheckpoint(checkpoint domain.StageCheckpoint) error {
	if strings.TrimSpace(checkpoint.Provider) == "" || strings.TrimSpace(checkpoint.RunID) == "" || strings.TrimSpace(checkpoint.StageID) == "" {
		return errors.New("checkpoint requires provider, run_id, and stage_id")
	}
	if checkpoint.Status == "" || checkpoint.InputHash == "" || checkpoint.RuntimeSnapshotHash == "" || checkpoint.ToolDefinitionsHash == "" {
		return errors.New("checkpoint requires status and state hashes")
	}
	return nil
}

func validateCheckpointUpdate(existing, next domain.StageCheckpoint) error {
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

func validateToolEffect(effect domain.ToolEffectRecord) error {
	if strings.TrimSpace(effect.IdempotencyKey) == "" || strings.TrimSpace(effect.RunID) == "" || strings.TrimSpace(effect.StageID) == "" || strings.TrimSpace(effect.ToolCallID) == "" || strings.TrimSpace(effect.ToolName) == "" || strings.TrimSpace(effect.RequestHash) == "" {
		return errors.New("tool effect requires idempotency, execution identity, and request hash")
	}
	return nil
}

func cloneToolEffect(effect domain.ToolEffectRecord) domain.ToolEffectRecord {
	effect.Result = append([]byte(nil), effect.Result...)
	return effect
}
