package memory

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/openai"
	"agentflow-platform/apps/api/internal/store"
)

type embeddingStub struct {
	embedding openai.Embedding
	err       error
}

func (e embeddingStub) EmbedText(context.Context, string) (openai.Embedding, error) {
	return e.embedding, e.err
}

func TestSemanticMemoryCreatesAndSearchesMemory(t *testing.T) {
	fileStore, err := store.NewFileStore(filepath.Join(t.TempDir(), "agentflow.json"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	semanticMemory := NewSemanticMemory(fileStore, embeddingStub{embedding: openai.Embedding{
		Vector: []float64{1, 0, 0}, Provider: "test", Model: "embedding-v1", Dimensions: 3,
	}})

	created, err := semanticMemory.Create(context.Background(), domain.Memory{Kind: " note ", Content: " durable fact "})
	if err != nil {
		t.Fatalf("create memory: %v", err)
	}
	if created.Kind != "note" || created.Content != "durable fact" {
		t.Fatalf("memory was not normalized: %#v", created)
	}

	items, err := semanticMemory.Search(context.Background(), domain.MemorySearch{Query: " durable fact ", Limit: 1})
	if err != nil {
		t.Fatalf("search memory: %v", err)
	}
	if len(items) != 1 || items[0].Memory.ID != created.ID {
		t.Fatalf("unexpected search result: %#v", items)
	}
}

func TestSemanticMemoryClassifiesEmbeddingFailure(t *testing.T) {
	want := errors.New("embedding provider unavailable")
	semanticMemory := NewSemanticMemory(nil, embeddingStub{err: want})

	_, err := semanticMemory.Search(context.Background(), domain.MemorySearch{Query: "fact"})
	if !IsEmbeddingError(err) || !errors.Is(err, want) {
		t.Fatalf("expected typed embedding error, got %v", err)
	}
}
