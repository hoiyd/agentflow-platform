package fixturestore

import (
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/store"
	"errors"
	"sort"
	"strings"
	"time"
)

func (s *Store) CreateChildRun(request domain.ChildRunRequest) (domain.Run, domain.RunDelegation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := request.Delegation
	if err := store.ValidateChildRunRequest(request); err != nil {
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
		ID: store.NewID("run"), WorkspaceID: parent.WorkspaceID, AgentID: d.AgentID,
		ConversationID: parent.ConversationID, Status: domain.RunQueued,
		RuntimeSnapshot:    store.CloneRuntimeSnapshot(request.RuntimeSnapshot),
		VerificationStatus: domain.VerificationNotRequired, CreatedAt: now, UpdatedAt: now,
	}
	d.WorkspaceID = parent.WorkspaceID
	d.ConversationID = parent.ConversationID
	d.ChildRunID = run.ID
	d.Status = domain.DelegationCreated
	d.Task = strings.TrimSpace(d.Task)
	d.CreatedAt = now
	d.UpdatedAt = now
	s.data.Runs = append(s.data.Runs, store.CloneRun(run))
	s.data.RunDelegations = append(s.data.RunDelegations, d)
	return store.CloneRun(run), d, nil
}

func (s *Store) UpdateRunDelegation(id string, result domain.DelegationResult) (domain.RunDelegation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := store.ValidateDelegationResult(result); err != nil {
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
		return *item, nil
	}
	return domain.RunDelegation{}, errors.New("delegation not found")
}

func (s *Store) GetRunDelegation(id string) (domain.RunDelegation, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, item := range s.data.RunDelegations {
		if item.ID == strings.TrimSpace(id) {
			return item, true, nil
		}
	}
	return domain.RunDelegation{}, false, nil
}

func (s *Store) GetParentDelegation(childRunID string) (domain.RunDelegation, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, item := range s.data.RunDelegations {
		if item.ChildRunID == strings.TrimSpace(childRunID) {
			return item, true, nil
		}
	}
	return domain.RunDelegation{}, false, nil
}

func (s *Store) ListRunDelegations(parentRunID string) ([]domain.RunDelegation, error) {
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

func (s *Store) ListActiveRunDelegations() ([]domain.RunDelegation, error) {
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
