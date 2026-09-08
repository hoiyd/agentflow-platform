package fixturestore

import (
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/store"

	"time"
)

func (s *Store) CreateModelRequestRecord(record domain.ModelRequestRecord) (domain.ModelRequestRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.hasRunLocked(record.Envelope.RunID) {
		return domain.ModelRequestRecord{}, store.ErrNotFound("run")
	}
	if err := store.ValidateModelRequestRecord(record); err != nil {
		return domain.ModelRequestRecord{}, err
	}
	record.Envelope.Attempt = store.NextModelRequestAttempt(s.data.ModelRequestRecords, record.Envelope.RunID, record.Envelope.ModelCallID)
	record = store.CloneModelRequestRecord(record)
	s.data.ModelRequestRecords = append(s.data.ModelRequestRecords, record)
	return store.CloneModelRequestRecord(record), nil
}

func (s *Store) ListModelRequestRecords(runID string) ([]domain.ModelRequestRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.hasRunLocked(runID) {
		return nil, store.ErrNotFound("run")
	}
	items := make([]domain.ModelRequestRecord, 0)
	for index, item := range s.data.ModelRequestRecords {
		if item.Envelope.RunID == runID {
			if store.ExpireModelRequestCapture(&item, time.Now().UTC()) {
				s.data.ModelRequestRecords[index] = item
			}
			items = append(items, store.CloneModelRequestRecord(item))
		}
	}
	store.SortModelRequestRecords(items)
	return items, nil
}
