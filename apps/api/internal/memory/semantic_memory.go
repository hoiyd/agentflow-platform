package memory

import (
	"context"
	"errors"
	"strings"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/failure"
)

type SemanticMemoryStore interface {
	CreateMemory(domain.Memory, domain.MemoryEmbedding) (domain.Memory, error)
	SearchMemories(domain.MemorySearch) ([]domain.RetrievedMemory, error)
}

type SemanticMemory struct {
	store    SemanticMemoryStore
	embedder Embedder
}

func NewSemanticMemory(store SemanticMemoryStore, embedder Embedder) *SemanticMemory {
	return &SemanticMemory{store: store, embedder: embedder}
}

// EmbeddingError identifies provider failures without leaking transport policy
// into the memory capability. The HTTP adapter maps this error to 502.
type EmbeddingError struct {
	Err error
}

func (e EmbeddingError) Error() string { return e.Err.Error() }
func (e EmbeddingError) Unwrap() error { return e.Err }
func (e EmbeddingError) FailureInfo() failure.Info {
	info := failure.Describe(e.Err)
	info.Source = "memory_embedding"
	return info
}

func IsEmbeddingError(err error) bool {
	var target EmbeddingError
	return errors.As(err, &target)
}

func (s *SemanticMemory) Create(ctx context.Context, item domain.Memory) (domain.Memory, error) {
	item.Kind = strings.TrimSpace(item.Kind)
	item.Content = strings.TrimSpace(item.Content)
	if item.Kind == "" {
		return domain.Memory{}, errors.New("memory kind is required")
	}
	if item.Content == "" {
		return domain.Memory{}, errors.New("memory content is required")
	}

	embedding, err := s.embedder.EmbedText(ctx, item.Content)
	if err != nil {
		return domain.Memory{}, EmbeddingError{Err: err}
	}
	return s.store.CreateMemory(item, domain.MemoryEmbedding{
		Provider:   embedding.Provider,
		Model:      embedding.Model,
		Dimensions: len(embedding.Vector),
		Embedding:  embedding.Vector,
	})
}

func (s *SemanticMemory) Search(ctx context.Context, search domain.MemorySearch) ([]domain.RetrievedMemory, error) {
	search.Query = strings.TrimSpace(search.Query)
	if search.Query == "" {
		return nil, errors.New("query is required")
	}

	embedding, err := s.embedder.EmbedText(ctx, search.Query)
	if err != nil {
		return nil, EmbeddingError{Err: err}
	}
	search.Embedding = embedding.Vector
	search.EmbeddingProvider = embedding.Provider
	search.EmbeddingModel = embedding.Model
	return s.store.SearchMemories(search)
}
