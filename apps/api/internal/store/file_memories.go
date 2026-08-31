package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/domain"
)

func (s *FileStore) CreateMemory(memory domain.Memory, embedding domain.MemoryEmbedding) (domain.Memory, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	memory.WorkspaceID = normalizeWorkspaceID(memory.WorkspaceID)
	memory.ID = strings.TrimSpace(memory.ID)
	if memory.ID == "" {
		memory.ID = newID("mem")
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

	for index := range s.data.Memories {
		if s.data.Memories[index].ID != memory.ID {
			continue
		}
		memory.CreatedAt = s.data.Memories[index].CreatedAt
		s.data.Memories[index] = memory
		replacedEmbedding := false
		for embeddingIndex := range s.data.MemoryEmbeddings {
			if s.data.MemoryEmbeddings[embeddingIndex].MemoryID == memory.ID {
				s.data.MemoryEmbeddings[embeddingIndex] = embedding
				replacedEmbedding = true
				break
			}
		}
		if !replacedEmbedding {
			s.data.MemoryEmbeddings = append(s.data.MemoryEmbeddings, embedding)
		}
		return memory, s.saveLocked()
	}

	s.data.Memories = append(s.data.Memories, memory)
	s.data.MemoryEmbeddings = append(s.data.MemoryEmbeddings, embedding)
	return memory, s.saveLocked()
}

func (s *FileStore) SearchMemories(search domain.MemorySearch) ([]domain.RetrievedMemory, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	search.WorkspaceID = normalizeWorkspaceID(search.WorkspaceID)

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
		if !memoryMatchesSearch(memory, search) {
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
		similarity := cosineSimilarity(search.Embedding, embedding.Embedding)
		recencyBoost := memoryRecencyBoost(now, memory.CreatedAt)
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

func memoryMatchesSearch(memory domain.Memory, search domain.MemorySearch) bool {
	if normalizeWorkspaceID(memory.WorkspaceID) != normalizeWorkspaceID(search.WorkspaceID) {
		return false
	}
	if search.UserID != "" && memory.UserID != search.UserID {
		return false
	}
	if search.ProjectID != "" && memory.ProjectID != search.ProjectID {
		return false
	}
	for key, expected := range search.Metadata {
		value, ok := memory.Metadata[key]
		if !ok || strings.TrimSpace(expected) != strings.TrimSpace(toString(value)) {
			return false
		}
	}
	return true
}

func cosineSimilarity(a []float64, b []float64) float64 {
	limit := len(a)
	if len(b) < limit {
		limit = len(b)
	}
	if limit == 0 {
		return 0
	}
	var dot, normA, normB float64
	for i := 0; i < limit; i++ {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}

func memoryRecencyBoost(now time.Time, createdAt time.Time) float64 {
	if createdAt.IsZero() {
		return 0
	}
	ageDays := now.Sub(createdAt).Hours() / 24
	if ageDays < 0 {
		ageDays = 0
	}
	return 0.05 / (1 + ageDays/30)
}

func toString(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case fmt.Stringer:
		return v.String()
	default:
		bytes, _ := json.Marshal(v)
		return string(bytes)
	}
}
