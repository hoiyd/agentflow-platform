package store

import (
	"errors"
	"sort"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/domain"
)

func (s *FileStore) CreateChildRun(request domain.ChildRunRequest) (domain.Run, domain.RunDelegation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := request.Delegation
	if err := validateChildRunRequest(request); err != nil {
		return domain.Run{}, domain.RunDelegation{}, err
	}
	parent, ok := s.getRunLocked(d.ParentRunID)
	if !ok {
		return domain.Run{}, domain.RunDelegation{}, errors.New("parent run not found")
	}
	if !s.hasAgentLocked(d.AgentID) {
		return domain.Run{}, domain.RunDelegation{}, errors.New("agent not found")
	}
	for _, existing := range s.data.RunDelegations {
		if existing.ID == d.ID {
			return domain.Run{}, domain.RunDelegation{}, errors.New("delegation already exists")
		}
	}
	now := time.Now().UTC()
	run := domain.Run{
		ID: newID("run"), WorkspaceID: parent.WorkspaceID, AgentID: d.AgentID,
		ConversationID: parent.ConversationID, Status: domain.RunQueued,
		RuntimeSnapshot:    cloneRuntimeSnapshot(request.RuntimeSnapshot),
		VerificationStatus: domain.VerificationNotRequired, CreatedAt: now, UpdatedAt: now,
	}
	d.WorkspaceID = parent.WorkspaceID
	d.ConversationID = parent.ConversationID
	d.ChildRunID = run.ID
	d.Status = domain.DelegationCreated
	d.Task = strings.TrimSpace(d.Task)
	d.CreatedAt = now
	d.UpdatedAt = now
	s.data.Runs = append(s.data.Runs, cloneRun(run))
	s.data.RunDelegations = append(s.data.RunDelegations, d)
	return cloneRun(run), d, s.saveLocked()
}

func (s *FileStore) UpdateRunDelegation(id string, result domain.DelegationResult) (domain.RunDelegation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := validateDelegationResult(result); err != nil {
		return domain.RunDelegation{}, err
	}
	for i := range s.data.RunDelegations {
		if s.data.RunDelegations[i].ID != strings.TrimSpace(id) {
			continue
		}
		item := &s.data.RunDelegations[i]
		item.Status = result.Status
		item.BlockReason = result.BlockReason
		item.Summary = strings.TrimSpace(result.Summary)
		item.OutputRef = strings.TrimSpace(result.OutputRef)
		item.OutputHash = strings.TrimSpace(result.OutputHash)
		item.OutputBytes = result.OutputBytes
		item.SummaryTruncated = result.SummaryTruncated
		item.Error = strings.TrimSpace(result.Error)
		item.UpdatedAt = time.Now().UTC()
		return *item, s.saveLocked()
	}
	return domain.RunDelegation{}, errors.New("delegation not found")
}

func validateChildRunRequest(request domain.ChildRunRequest) error {
	d := request.Delegation
	snapshot := request.RuntimeSnapshot
	frozen := snapshot.Delegation
	if strings.TrimSpace(d.ID) == "" || strings.TrimSpace(d.ParentRunID) == "" || strings.TrimSpace(d.ParentTurnID) == "" || strings.TrimSpace(d.AgentID) == "" || strings.TrimSpace(d.Task) == "" {
		return errors.New("delegation id, parent, agent, and task are required")
	}
	if d.Depth != 1 || d.TimeoutMS <= 0 || frozen == nil || frozen.DelegationID != d.ID || frozen.ParentRunID != d.ParentRunID || frozen.ParentTurnID != d.ParentTurnID || frozen.Depth != d.Depth || frozen.TimeoutMS != d.TimeoutMS || !frozen.IsolatedContext {
		return errors.New("invalid child run delegation snapshot")
	}
	if snapshot.SchemaVersion != domain.CurrentRuntimeSnapshotVersion || snapshot.Mode != "single" || snapshot.RunBudget == nil || snapshot.Agent.ID != d.AgentID {
		return errors.New("runtime snapshot is required")
	}
	return nil
}

func validateDelegationResult(result domain.DelegationResult) error {
	switch result.Status {
	case domain.DelegationCreated, domain.DelegationRunning,
		domain.DelegationCompleted, domain.DelegationFailed, domain.DelegationCanceled:
		if result.BlockReason != "" {
			return errors.New("delegation block reason requires blocked status")
		}
		return nil
	case domain.DelegationBlocked:
		if result.BlockReason != domain.DelegationBlockReasonChildRecoveryRequired {
			return errors.New("delegation block reason is invalid")
		}
		return nil
	default:
		return errors.New("delegation status is invalid")
	}
}

func (s *FileStore) GetRunDelegation(id string) (domain.RunDelegation, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, item := range s.data.RunDelegations {
		if item.ID == strings.TrimSpace(id) {
			return item, true, nil
		}
	}
	return domain.RunDelegation{}, false, nil
}

func (s *FileStore) GetParentDelegation(childRunID string) (domain.RunDelegation, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, item := range s.data.RunDelegations {
		if item.ChildRunID == strings.TrimSpace(childRunID) {
			return item, true, nil
		}
	}
	return domain.RunDelegation{}, false, nil
}

func (s *FileStore) ListRunDelegations(parentRunID string) ([]domain.RunDelegation, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := []domain.RunDelegation{}
	for _, item := range s.data.RunDelegations {
		if item.ParentRunID == strings.TrimSpace(parentRunID) {
			items = append(items, item)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.Before(items[j].CreatedAt) })
	return items, nil
}

func (s *FileStore) ListActiveRunDelegations() ([]domain.RunDelegation, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := []domain.RunDelegation{}
	for _, item := range s.data.RunDelegations {
		if item.Status == domain.DelegationCreated || item.Status == domain.DelegationRunning {
			items = append(items, item)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.Before(items[j].CreatedAt) })
	return items, nil
}
