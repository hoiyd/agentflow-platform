package store

import (
	"slices"

	"agentflow-platform/apps/api/internal/domain"
)

func (s *FileStore) FindMemoryChange(workspaceID, operationID string) (*domain.MemoryChange, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, change := range s.data.MemoryChanges {
		if change.WorkspaceID == normalizeWorkspaceID(workspaceID) && change.OperationID == operationID {
			return &change, nil
		}
	}
	return nil, nil
}

func (s *FileStore) GetMemoryDetail(workspaceID, id string) (domain.MemoryDetail, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, item := range s.data.Memories {
		if item.ID != id || normalizeWorkspaceID(item.WorkspaceID) != normalizeWorkspaceID(workspaceID) {
			continue
		}
		item.Version = max(1, item.Version)
		item.WorkspaceID = normalizeWorkspaceID(item.WorkspaceID)
		detail := domain.MemoryDetail{Memory: item, Changes: []domain.MemoryChange{}}
		for i := len(s.data.MemoryChanges) - 1; i >= 0 && len(detail.Changes) < 100; i-- {
			change := s.data.MemoryChanges[i]
			if change.MemoryID == id && change.WorkspaceID == item.WorkspaceID {
				detail.Changes = append(detail.Changes, change)
			}
		}
		return detail, nil
	}
	return domain.MemoryDetail{}, ErrMemoryMissing
}

func (s *FileStore) MutateMemory(workspaceID, id string, command domain.MemoryMutation, embedding domain.MemoryEmbedding) (domain.MemoryMutationResult, error) {
	command, err := NormalizeMemoryMutation(command)
	if err != nil {
		return domain.MemoryMutationResult{}, err
	}
	workspaceID = normalizeWorkspaceID(workspaceID)
	s.mu.Lock()
	defer s.mu.Unlock()
	index := -1
	for i, item := range s.data.Memories {
		if item.ID == id && normalizeWorkspaceID(item.WorkspaceID) == workspaceID {
			index = i
			break
		}
	}
	if index < 0 {
		return domain.MemoryMutationResult{}, ErrMemoryMissing
	}
	current := s.data.Memories[index]
	current.WorkspaceID, current.Version = workspaceID, max(1, current.Version)
	for _, previous := range s.data.MemoryChanges {
		if previous.WorkspaceID != workspaceID || previous.OperationID != command.OperationID {
			continue
		}
		if previous.MemoryID != id || previous.CommandHash != memoryCommandHash(id, command) {
			return domain.MemoryMutationResult{}, ErrMemoryConflict
		}
		return domain.MemoryMutationResult{Memory: current, Change: previous, Applied: false}, nil
	}
	result, err := prepareMemoryMutation(current, command)
	if err != nil {
		return domain.MemoryMutationResult{}, err
	}
	if command.Action == "replace" && (len(embedding.Embedding) == 0 || embedding.Dimensions != len(embedding.Embedding)) {
		return domain.MemoryMutationResult{}, ErrMemoryMutationInvalid
	}
	oldMemories := slices.Clone(s.data.Memories)
	oldCandidates := slices.Clone(s.data.MemoryCandidates)
	oldEmbeddings, oldChanges := s.data.MemoryEmbeddings, s.data.MemoryChanges
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
				s.data.MemoryCandidates[i] = suppressMemoryCandidate(candidate)
			}
		}
	}
	s.data.MemoryChanges = append(s.data.MemoryChanges, result.Change)
	if err := s.saveLocked(); err != nil {
		s.data.Memories, s.data.MemoryCandidates, s.data.MemoryEmbeddings, s.data.MemoryChanges = oldMemories, oldCandidates, oldEmbeddings, oldChanges
		return domain.MemoryMutationResult{}, err
	}
	return result, nil
}
