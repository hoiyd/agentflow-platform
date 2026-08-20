package store

import (
	"errors"
	"sort"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/domain"
)

func (s *FileStore) CreateRun(agentID string, conversationID string, snapshot domain.RuntimeSnapshot) (domain.Run, error) {
	return s.CreateRunWithContract(agentID, conversationID, snapshot, nil)
}

func (s *FileStore) CreateRunWithContract(agentID string, conversationID string, snapshot domain.RuntimeSnapshot, contract *domain.CompletionContract) (domain.Run, error) {
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
		ID:                 newID("run"),
		WorkspaceID:        conversation.WorkspaceID,
		AgentID:            agentID,
		ConversationID:     conversationID,
		Status:             domain.RunQueued,
		RuntimeSnapshot:    cloneRuntimeSnapshot(snapshot),
		CompletionContract: cloneCompletionContract(contract),
		VerificationStatus: domain.VerificationNotRequired,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	if contract != nil {
		run.VerificationStatus = domain.VerificationPending
	}
	stored := cloneRun(run)
	s.data.Runs = append(s.data.Runs, stored)
	return cloneRun(run), s.saveLocked()
}

func (s *FileStore) UpdateRunVerificationStatus(id string, status domain.VerificationStatus) (domain.Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.data.Runs {
		if s.data.Runs[i].ID == id {
			s.data.Runs[i].VerificationStatus = status
			s.data.Runs[i].UpdatedAt = time.Now().UTC()
			return cloneRun(s.data.Runs[i]), s.saveLocked()
		}
	}
	return domain.Run{}, errors.New("run not found")
}

func (s *FileStore) AppendVerificationRecord(record domain.VerificationRecord) error {
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
	s.data.VerificationEvidence = append(s.data.VerificationEvidence, cloneVerificationEvidence(record.Evidence))
	s.data.VerificationArtifacts = append(s.data.VerificationArtifacts, record.Artifacts...)
	return s.saveLocked()
}

func (s *FileStore) ListVerificationEvidence(runID string) ([]domain.VerificationEvidence, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := []domain.VerificationEvidence{}
	for _, item := range s.data.VerificationEvidence {
		if item.RunID == runID {
			items = append(items, cloneVerificationEvidence(item))
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].StartedAt.Before(items[j].StartedAt) })
	return items, nil
}

func (s *FileStore) ListVerificationArtifacts(runID string) ([]domain.VerificationArtifact, error) {
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

func (s *FileStore) UpdateRunAgent(id string, agentID string) (domain.Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.hasAgentLocked(agentID) {
		return domain.Run{}, errors.New("agent not found")
	}
	for i := range s.data.Runs {
		if s.data.Runs[i].ID == id {
			s.data.Runs[i].AgentID = agentID
			s.data.Runs[i].UpdatedAt = time.Now().UTC()
			return cloneRun(s.data.Runs[i]), s.saveLocked()
		}
	}
	return domain.Run{}, errors.New("run not found")
}

func (s *FileStore) UpdateRunStatus(id string, status domain.RunStatus, errorMessage string) (domain.Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.data.Runs {
		if s.data.Runs[i].ID == id {
			applyRunStatus(&s.data.Runs[i], status, errorMessage, time.Now().UTC())
			return cloneRun(s.data.Runs[i]), s.saveLocked()
		}
	}
	return domain.Run{}, errors.New("run not found")
}

func applyRunStatus(run *domain.Run, status domain.RunStatus, errorMessage string, now time.Time) {
	wasRunning := run.Status == domain.RunRunning
	willRun := status == domain.RunRunning
	if wasRunning && !willRun && run.ExecutionStartedAt != nil {
		run.ActiveRuntimeMS += max(int64(0), now.Sub(*run.ExecutionStartedAt).Milliseconds())
		run.ExecutionStartedAt = nil
	}
	if !wasRunning && willRun {
		run.ExecutionStartedAt = &now
	}
	run.Status = status
	run.Error = strings.TrimSpace(errorMessage)
	run.UpdatedAt = now
	if status == domain.RunRunning && run.StartedAt == nil {
		run.StartedAt = &now
	}
	if status == domain.RunRunning {
		run.HeartbeatAt = &now
		run.CompletedAt = nil
	}
	if status == domain.RunWaitingForUser {
		run.CompletedAt = nil
	}
	if status == domain.RunCompleted || status == domain.RunFailed || status == domain.RunFailedRecoverable || status == domain.RunCanceled {
		run.CompletedAt = &now
	}
}

func (s *FileStore) UpdateRunHeartbeat(id string) (domain.Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.data.Runs {
		if s.data.Runs[i].ID == id {
			now := time.Now().UTC()
			s.data.Runs[i].HeartbeatAt = &now
			s.data.Runs[i].UpdatedAt = now
			return cloneRun(s.data.Runs[i]), s.saveLocked()
		}
	}
	return domain.Run{}, errors.New("run not found")
}

func (s *FileStore) ListStaleRunningRuns(cutoff time.Time) ([]domain.Run, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	items := []domain.Run{}
	for _, run := range s.data.Runs {
		if run.Status != domain.RunRunning {
			continue
		}
		if run.HeartbeatAt == nil || run.HeartbeatAt.Before(cutoff) {
			items = append(items, cloneRun(run))
		}
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].CreatedAt.Before(items[j].CreatedAt)
	})
	return items, nil
}

func (s *FileStore) GetRun(id string) (domain.Run, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, item := range s.data.Runs {
		if item.ID == id {
			return cloneRun(item), true, nil
		}
	}
	return domain.Run{}, false, nil
}

func (s *FileStore) GetRunInWorkspace(workspaceID string, id string) (domain.Run, bool, error) {
	run, ok, err := s.GetRun(id)
	if err != nil || !ok || run.WorkspaceID != normalizeWorkspaceID(workspaceID) {
		return domain.Run{}, false, err
	}
	return run, true, nil
}

func (s *FileStore) ListRuns() ([]domain.Run, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.data.Runs) == 0 {
		return []domain.Run{}, nil
	}
	items := make([]domain.Run, 0, len(s.data.Runs))
	for _, run := range s.data.Runs {
		items = append(items, cloneRun(run))
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].CreatedAt.After(items[j].CreatedAt)
	})
	return items, nil
}

func (s *FileStore) ListRunsByWorkspace(workspaceID string) ([]domain.Run, error) {
	runs, err := s.ListRuns()
	if err != nil {
		return nil, err
	}
	workspaceID = normalizeWorkspaceID(workspaceID)
	items := make([]domain.Run, 0, len(runs))
	for _, run := range runs {
		if run.WorkspaceID == workspaceID {
			items = append(items, run)
		}
	}
	return items, nil
}
