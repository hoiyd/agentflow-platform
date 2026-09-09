package fixturestore

import (
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/store"
	"errors"
	"sort"

	"time"
)

func (s *Store) CreateRunWithContract(agentID string, conversationID string, snapshot domain.RuntimeSnapshot, contract *domain.CompletionContract) (domain.Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.hasAgentLocked(agentID) {
		return domain.Run{}, errors.New("agent not found")
	}
	if !s.hasConversationLocked(conversationID) {
		return domain.Run{}, errors.New("conversation not found")
	}
	conversation, _ := s.getConversationLocked(conversationID)
	if snapshot.SchemaVersion != domain.CurrentRuntimeSnapshotVersion || snapshot.RunBudget == nil {
		return domain.Run{}, errors.New("runtime snapshot is required")
	}

	now := time.Now().UTC()
	run := domain.Run{
		ID:                 store.NewID("run"),
		WorkspaceID:        conversation.WorkspaceID,
		AgentID:            agentID,
		ConversationID:     conversationID,
		Status:             domain.RunQueued,
		RuntimeSnapshot:    store.CloneRuntimeSnapshot(snapshot),
		CompletionContract: store.CloneCompletionContract(contract),
		VerificationStatus: domain.VerificationNotRequired,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	if contract != nil {
		run.VerificationStatus = domain.VerificationPending
	}
	stored := store.CloneRun(run)
	s.data.Runs = append(s.data.Runs, stored)
	return store.CloneRun(run), nil
}

func (s *Store) UpdateRunVerificationStatus(id string, status domain.VerificationStatus) (domain.Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.data.Runs {
		if s.data.Runs[i].ID == id {
			s.data.Runs[i].VerificationStatus = status
			s.data.Runs[i].UpdatedAt = time.Now().UTC()
			return store.CloneRun(s.data.Runs[i]), nil
		}
	}
	return domain.Run{}, errors.New("run not found")
}

func (s *Store) AppendVerificationRecord(record domain.VerificationRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.getRunLocked(record.Evidence.RunID); !ok {
		return errors.New("run not found")
	}
	for _, item := range s.data.VerificationEvidence {
		if item.ID == record.Evidence.ID {
			return errors.New("verification evidence already exists")
		}
	}
	for _, artifact := range record.Artifacts {
		if artifact.RunID != record.Evidence.RunID || artifact.EvidenceID != record.Evidence.ID {
			return errors.New("verification artifact does not match evidence")
		}
	}
	s.data.VerificationEvidence = append(s.data.VerificationEvidence, store.CloneVerificationEvidence(record.Evidence))
	s.data.VerificationArtifacts = append(s.data.VerificationArtifacts, record.Artifacts...)
	return nil
}

func (s *Store) ListVerificationEvidence(runID string) ([]domain.VerificationEvidence, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := []domain.VerificationEvidence{}
	for _, item := range s.data.VerificationEvidence {
		if item.RunID == runID {
			items = append(items, store.CloneVerificationEvidence(item))
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].StartedAt.Before(items[j].StartedAt) })
	return items, nil
}

func (s *Store) ListVerificationArtifacts(runID string) ([]domain.VerificationArtifact, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := []domain.VerificationArtifact{}
	for _, item := range s.data.VerificationArtifacts {
		if item.RunID == runID {
			items = append(items, item)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.Before(items[j].CreatedAt) })
	return items, nil
}

func (s *Store) UpdateRunAgent(id string, agentID string) (domain.Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.hasAgentLocked(agentID) {
		return domain.Run{}, errors.New("agent not found")
	}
	for i := range s.data.Runs {
		if s.data.Runs[i].ID == id {
			s.data.Runs[i].AgentID = agentID
			s.data.Runs[i].UpdatedAt = time.Now().UTC()
			return store.CloneRun(s.data.Runs[i]), nil
		}
	}
	return domain.Run{}, errors.New("run not found")
}

func (s *Store) UpdateRunStatus(id string, status domain.RunStatus, errorMessage string) (domain.Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.data.Runs {
		if s.data.Runs[i].ID == id {
			store.ApplyRunStatus(&s.data.Runs[i], status, errorMessage, time.Now().UTC())
			return store.CloneRun(s.data.Runs[i]), nil
		}
	}
	return domain.Run{}, errors.New("run not found")
}

func (s *Store) UpdateRunHeartbeat(id string) (domain.Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.data.Runs {
		if s.data.Runs[i].ID == id {
			now := time.Now().UTC()
			s.data.Runs[i].HeartbeatAt = &now
			s.data.Runs[i].UpdatedAt = now
			return store.CloneRun(s.data.Runs[i]), nil
		}
	}
	return domain.Run{}, errors.New("run not found")
}

func (s *Store) ListStaleRunningRuns(cutoff time.Time) ([]domain.Run, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	items := []domain.Run{}
	for _, run := range s.data.Runs {
		if run.Status != domain.RunRunning {
			continue
		}
		if run.HeartbeatAt == nil || run.HeartbeatAt.Before(cutoff) {
			items = append(items, store.CloneRun(run))
		}
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].CreatedAt.Before(items[j].CreatedAt)
	})
	return items, nil
}

func (s *Store) GetRun(id string) (domain.Run, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, item := range s.data.Runs {
		if item.ID == id {
			return store.CloneRun(item), true, nil
		}
	}
	return domain.Run{}, false, nil
}

func (s *Store) GetRunInWorkspace(workspaceID string, id string) (domain.Run, bool, error) {
	run, ok, err := s.GetRun(id)
	if err != nil || !ok || run.WorkspaceID != store.NormalizeWorkspaceID(workspaceID) {
		return domain.Run{}, false, err
	}
	return run, true, nil
}

func (s *Store) ListRuns() ([]domain.Run, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.data.Runs) == 0 {
		return []domain.Run{}, nil
	}
	items := make([]domain.Run, 0, len(s.data.Runs))
	for _, run := range s.data.Runs {
		items = append(items, store.CloneRun(run))
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].CreatedAt.After(items[j].CreatedAt)
	})
	return items, nil
}

func (s *Store) ListRunsByWorkspace(workspaceID string) ([]domain.Run, error) {
	runs, err := s.ListRuns()
	if err != nil {
		return nil, err
	}
	workspaceID = store.NormalizeWorkspaceID(workspaceID)
	items := make([]domain.Run, 0, len(runs))
	for _, run := range runs {
		if run.WorkspaceID == workspaceID {
			items = append(items, run)
		}
	}
	return items, nil
}
