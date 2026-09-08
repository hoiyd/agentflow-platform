package fixturestore

import (
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/store"

	"errors"

	"sort"
	"strings"
	"time"
)

func (s *Store) CreateMemory(memory domain.Memory, embedding domain.MemoryEmbedding) (domain.Memory, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	memory.WorkspaceID = store.NormalizeWorkspaceID(memory.WorkspaceID)
	memory.ID = strings.TrimSpace(memory.ID)
	if memory.ID == "" {
		memory.ID = store.NewID("mem")
	}
	memory.Kind = strings.TrimSpace(memory.Kind)
	if memory.Kind == "" {
		return domain.Memory{}, errors.New("memory kind is required")
	}
	memory.Content = strings.TrimSpace(memory.Content)
	if memory.Content == "" {
		return domain.Memory{}, errors.New("memory content is required")
	}
	if memory.Metadata == nil {
		memory.Metadata = map[string]any{}
	}
	if memory.CreatedAt.IsZero() {
		memory.CreatedAt = now
	}
	memory.UpdatedAt = now
	memory.Version, memory.DeletedAt = 1, nil
	embedding.MemoryID = memory.ID
	if embedding.Provider == "" {
		embedding.Provider = "local"
	}
	if embedding.Model == "" {
		embedding.Model = "local_hash"
	}
	if embedding.Dimensions == 0 {
		embedding.Dimensions = len(embedding.Embedding)
	}
	if embedding.CreatedAt.IsZero() {
		embedding.CreatedAt = now
	}

	for _, existing := range s.data.MemoryChanges {
		if memory.SourceMessageID != "" && existing.SourceMessageID == memory.SourceMessageID && existing.WorkspaceID == memory.WorkspaceID {
			return domain.Memory{}, store.ErrMemoryConflict
		}
	}
	for _, existing := range s.data.Memories {
		if existing.ID == memory.ID {
			if !store.SameMemoryCreate(existing, memory) {
				return domain.Memory{}, store.ErrMemoryConflict
			}
			existing.Version = max(1, existing.Version)
			return existing, nil
		}
	}

	s.data.Memories = append(s.data.Memories, memory)
	s.data.MemoryEmbeddings = append(s.data.MemoryEmbeddings, embedding)

	return memory, nil
}

func (s *Store) SearchMemories(search domain.MemorySearch) ([]domain.RetrievedMemory, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	search.WorkspaceID = store.NormalizeWorkspaceID(search.WorkspaceID)

	limit := search.Limit
	if limit <= 0 {
		limit = 5
	} else if limit > 20 {
		limit = 20
	}
	embeddingByMemoryID := map[string]domain.MemoryEmbedding{}
	for _, embedding := range s.data.MemoryEmbeddings {
		embeddingByMemoryID[embedding.MemoryID] = embedding
	}

	items := []domain.RetrievedMemory{}
	now := time.Now().UTC()
	for _, memory := range s.data.Memories {
		memory.Version = max(1, memory.Version)
		if !store.MemoryMatchesSearch(memory, search) {
			continue
		}
		embedding, ok := embeddingByMemoryID[memory.ID]
		if !ok || len(embedding.Embedding) == 0 || len(search.Embedding) == 0 {
			continue
		}
		if search.EmbeddingProvider != "" && embedding.Provider != search.EmbeddingProvider {
			continue
		}
		if search.EmbeddingModel != "" && embedding.Model != search.EmbeddingModel {
			continue
		}
		similarity := store.CosineSimilarity(search.Embedding, embedding.Embedding)
		recencyBoost := store.MemoryRecencyBoost(now, memory.CreatedAt)
		items = append(items, domain.RetrievedMemory{
			Memory:       memory,
			Similarity:   similarity,
			RecencyBoost: recencyBoost,
			Score:        similarity + recencyBoost,
		})
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].Score > items[j].Score
	})
	if len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}
