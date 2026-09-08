package fixturestore

import (
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/store"
)

func (s *Store) FindMemoryChange(workspaceID, operationID string) (*domain.MemoryChange, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, change := range s.data.MemoryChanges {
		if change.WorkspaceID == store.NormalizeWorkspaceID(workspaceID) && change.OperationID == operationID {
			return &change, nil
		}
	}
	return nil, nil
}

func (s *Store) GetMemoryDetail(workspaceID, id string) (domain.MemoryDetail, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, item := range s.data.Memories {
		if item.ID != id || store.NormalizeWorkspaceID(item.WorkspaceID) != store.NormalizeWorkspaceID(workspaceID) {
			continue
		}
		item.Version = max(1, item.Version)
		item.WorkspaceID = store.NormalizeWorkspaceID(item.WorkspaceID)
		detail := domain.MemoryDetail{Memory: item, Changes: []domain.MemoryChange{}}
		for i := len(s.data.MemoryChanges) - 1; i >= 0 && len(detail.Changes) < 100; i-- {
			change := s.data.MemoryChanges[i]
			if change.MemoryID == id && change.WorkspaceID == item.WorkspaceID {
				detail.Changes = append(detail.Changes, change)
			}
		}
		return detail, nil
	}
	return domain.MemoryDetail{}, store.ErrMemoryMissing
}

func (s *Store) MutateMemory(workspaceID, id string, command domain.MemoryMutation, embedding domain.MemoryEmbedding) (domain.MemoryMutationResult, error) {
	command, err := store.NormalizeMemoryMutation(command)
	if err != nil {
		return domain.MemoryMutationResult{}, err
	}
	workspaceID = store.NormalizeWorkspaceID(workspaceID)
	s.mu.Lock()
	defer s.mu.Unlock()
	index := -1
	for i, item := range s.data.Memories {
		if item.ID == id && store.NormalizeWorkspaceID(item.WorkspaceID) == workspaceID {
			index = i
			break
		}
	}
	if index < 0 {
		return domain.MemoryMutationResult{}, store.ErrMemoryMissing
	}
	current := s.data.Memories[index]
	current.WorkspaceID, current.Version = workspaceID, max(1, current.Version)
	for _, previous := range s.data.MemoryChanges {
		if previous.WorkspaceID != workspaceID || previous.OperationID != command.OperationID {
			continue
		}
		if previous.MemoryID != id || previous.CommandHash != store.MemoryCommandHash(id, command) {
			return domain.MemoryMutationResult{}, store.ErrMemoryConflict
		}
		return domain.MemoryMutationResult{Memory: current, Change: previous, Applied: false}, nil
	}
	result, err := store.PrepareMemoryMutation(current, command)
	if err != nil {
		return domain.MemoryMutationResult{}, err
	}
	if command.Action == "replace" && (len(embedding.Embedding) == 0 || embedding.Dimensions != len(embedding.Embedding)) {
		return domain.MemoryMutationResult{}, store.ErrMemoryMutationInvalid
	}
	oldEmbeddings := s.data.MemoryEmbeddings
	s.data.Memories[index] = result.Memory
	s.data.MemoryEmbeddings = make([]domain.MemoryEmbedding, 0, len(oldEmbeddings))
	for _, item := range oldEmbeddings {
		if item.MemoryID != id {
			s.data.MemoryEmbeddings = append(s.data.MemoryEmbeddings, item)
		}
	}
	if command.Action == "replace" {
		embedding.MemoryID = id
		embedding.CreatedAt = result.Change.CreatedAt
		s.data.MemoryEmbeddings = append(s.data.MemoryEmbeddings, embedding)
	}
	if current.SourceMessageID != "" {
		for i, candidate := range s.data.MemoryCandidates {
			if candidate.SourceMessageID == current.SourceMessageID && candidate.WorkspaceID == workspaceID {
				s.data.MemoryCandidates[i] = store.SuppressMemoryCandidate(candidate)
			}
		}
	}
	s.data.MemoryChanges = append(s.data.MemoryChanges, result.Change)

	return result, nil
}
