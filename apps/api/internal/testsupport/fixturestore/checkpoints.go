package fixturestore

import (
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/store"
	"bytes"
	"errors"
	"sort"
	"strings"
	"time"
)

func (s *Store) SaveStageCheckpoint(checkpoint domain.StageCheckpoint) (domain.StageCheckpoint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := store.ValidateStageCheckpoint(checkpoint); err != nil {
		return domain.StageCheckpoint{}, err
	}
	now := time.Now().UTC()
	for index := range s.data.StageCheckpoints {
		existing := s.data.StageCheckpoints[index]
		if existing.RunID != checkpoint.RunID || existing.StageID != checkpoint.StageID {
			continue
		}
		if err := store.ValidateCheckpointUpdate(existing, checkpoint); err != nil {
			return domain.StageCheckpoint{}, err
		}
		checkpoint.ID = existing.ID
		checkpoint.CreatedAt = existing.CreatedAt
		checkpoint.UpdatedAt = now
		s.data.StageCheckpoints[index] = checkpoint

		return checkpoint, nil
	}
	if checkpoint.ID == "" {
		checkpoint.ID = store.NewID("checkpoint")
	}
	checkpoint.CreatedAt = now
	checkpoint.UpdatedAt = now
	s.data.StageCheckpoints = append(s.data.StageCheckpoints, checkpoint)

	return checkpoint, nil
}

func (s *Store) GetStageCheckpoint(runID string, stageID string) (domain.StageCheckpoint, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, item := range s.data.StageCheckpoints {
		if item.RunID == runID && item.StageID == stageID {
			return item, true, nil
		}
	}
	return domain.StageCheckpoint{}, false, nil
}

func (s *Store) ListStageCheckpoints(runID string) ([]domain.StageCheckpoint, error) {
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

func (s *Store) BeginToolEffect(effect domain.ToolEffectRecord) (domain.ToolEffectRecord, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := store.ValidateToolEffect(effect); err != nil {
		return domain.ToolEffectRecord{}, false, err
	}
	for _, existing := range s.data.ToolEffects {
		if existing.IdempotencyKey != effect.IdempotencyKey {
			continue
		}
		if existing.RequestHash != effect.RequestHash || existing.ToolName != effect.ToolName || existing.RunID != effect.RunID {
			return domain.ToolEffectRecord{}, false, errors.New("idempotency key was already used for a different tool request")
		}
		return store.CloneToolEffect(existing), false, nil
	}
	now := time.Now().UTC()
	effect.Status = domain.ToolEffectExecuting
	effect.Version = 1
	effect.CreatedAt = now
	effect.UpdatedAt = now
	s.data.ToolEffects = append(s.data.ToolEffects, store.CloneToolEffect(effect))

	return store.CloneToolEffect(effect), true, nil
}

func (s *Store) CompleteToolEffect(idempotencyKey string, result []byte) (domain.ToolEffectRecord, error) {
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
			return store.CloneToolEffect(*item), nil
		}
		if item.Status != domain.ToolEffectExecuting {
			return domain.ToolEffectRecord{}, errors.New("tool effect is not executing")
		}
		item.Status = domain.ToolEffectCommitted
		item.Version = max(item.Version, 1) + 1
		item.Result = append([]byte(nil), result...)
		item.Error = ""
		item.UpdatedAt = time.Now().UTC()
		stored := store.CloneToolEffect(*item)

		return stored, nil
	}
	return domain.ToolEffectRecord{}, store.ErrNotFound("tool effect")
}

func (s *Store) MarkToolEffectNeedsReconciliation(idempotencyKey string, errorMessage string) (domain.ToolEffectRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for index := range s.data.ToolEffects {
		item := &s.data.ToolEffects[index]
		if item.IdempotencyKey != idempotencyKey {
			continue
		}
		if item.Status != domain.ToolEffectExecuting && item.Status != domain.ToolEffectPrepared && item.Status != domain.ToolEffectNeedsReconciliation {
			return domain.ToolEffectRecord{}, errors.New("tool effect cannot accept a late execution failure")
		}
		item.Status = domain.ToolEffectNeedsReconciliation
		item.Version = max(item.Version, 1) + 1
		item.Error = strings.TrimSpace(errorMessage)
		item.UpdatedAt = time.Now().UTC()
		stored := store.CloneToolEffect(*item)

		return stored, nil
	}
	return domain.ToolEffectRecord{}, store.ErrNotFound("tool effect")
}

func (s *Store) ListToolEffects(runID string) ([]domain.ToolEffectRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]domain.ToolEffectRecord, 0)
	for _, item := range s.data.ToolEffects {
		if item.RunID == runID {
			items = append(items, store.CloneToolEffect(item))
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.Before(items[j].CreatedAt) })
	return items, nil
}

func (s *Store) CommitToolEffectReconciliation(mutation domain.ToolEffectReconciliation) (domain.ToolEffectRecord, domain.RunEvent, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existingEvent := range s.data.RunEvents {
		if existingEvent.ID == mutation.Event.ID {
			for _, effect := range s.data.ToolEffects {
				if effect.IdempotencyKey == mutation.IdempotencyKey {
					if err := store.ValidateReconciliationDuplicate(existingEvent, mutation); err != nil {
						return domain.ToolEffectRecord{}, domain.RunEvent{}, false, err
					}
					return store.CloneToolEffect(effect), domain.RunEvent{}, false, nil
				}
			}
			return domain.ToolEffectRecord{}, domain.RunEvent{}, false, store.ErrNotFound("tool effect")
		}
	}
	for index := range s.data.ToolEffects {
		current := store.CloneToolEffect(s.data.ToolEffects[index])
		if current.IdempotencyKey != mutation.IdempotencyKey {
			continue
		}
		prepared, err := store.PrepareToolEffectReconciliation(current, mutation)
		if err != nil {
			return domain.ToolEffectRecord{}, domain.RunEvent{}, false, err
		}
		var next int64 = 1
		for _, event := range s.data.RunEvents {
			if event.RunID == current.RunID && event.Sequence >= next {
				next = event.Sequence + 1
			}
		}
		createdEvent, err := store.PrepareRunEvent(prepared.Event, next, time.Now().UTC())
		if err != nil {
			return domain.ToolEffectRecord{}, domain.RunEvent{}, false, err
		}
		updated := current
		updated.Version++
		updated.Status = prepared.NextStatus
		updated.Result = append([]byte(nil), prepared.Result...)
		updated.Error = strings.TrimSpace(prepared.Error)
		updated.UpdatedAt = createdEvent.Timestamp
		s.data.ToolEffects[index] = updated
		s.data.RunEvents = append(s.data.RunEvents, createdEvent)

		return store.CloneToolEffect(updated), createdEvent, true, nil
	}
	return domain.ToolEffectRecord{}, domain.RunEvent{}, false, store.ErrNotFound("tool effect")
}
