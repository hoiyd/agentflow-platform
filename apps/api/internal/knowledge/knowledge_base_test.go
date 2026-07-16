package knowledge

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

func TestKnowledgeBaseIngestsSearchesAndEvaluatesKnowledge(t *testing.T) {
	fileStore, err := store.NewFileStore(filepath.Join(t.TempDir(), "agentflow.json"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	knowledgeBase := NewKnowledgeBase(fileStore, embeddingStub{embedding: openai.Embedding{
		Vector: []float64{1, 0, 0}, Provider: "test", Model: "embedding-v1", Dimensions: 3,
	}})

	document, err := knowledgeBase.Ingest(context.Background(), domain.DocumentIngestRequest{
		Title: "Launch notes", Content: "The launch password is amber-9137.",
	})
	if err != nil {
		t.Fatalf("ingest document: %v", err)
	}
	if document.ChunkCount != 1 || document.EmbeddingCount != 1 {
		t.Fatalf("unexpected ingest metadata: %#v", document)
	}

	response, err := knowledgeBase.Search(context.Background(), domain.DocumentSearch{
		Query: "launch password", Limit: 3,
	}, 3)
	if err != nil {
		t.Fatalf("search knowledge: %v", err)
	}
	if len(response.Items) != 1 || response.Embedding.Provider != "test" {
		t.Fatalf("unexpected search response: %#v", response)
	}

	evaluation, err := knowledgeBase.Evaluate(context.Background(), domain.RAGEvaluationRunRequest{
		Cases: []domain.RAGEvaluationCase{{
			ID: "launch", Query: "launch password", ExpectedDocumentIDs: []string{document.ID},
		}},
	})
	if err != nil {
		t.Fatalf("evaluate knowledge: %v", err)
	}
	if evaluation.Summary.HitAt1 != 1 || evaluation.Summary.Misses != 0 {
		t.Fatalf("unexpected evaluation summary: %#v", evaluation.Summary)
	}
}

func TestKnowledgeBaseClassifiesEmbeddingFailure(t *testing.T) {
	want := errors.New("embedding provider unavailable")
	knowledgeBase := NewKnowledgeBase(nil, embeddingStub{err: want})

	_, err := knowledgeBase.Search(context.Background(), domain.DocumentSearch{Query: "launch"}, 1)
	if !IsEmbeddingError(err) || !errors.Is(err, want) {
		t.Fatalf("expected typed embedding error, got %v", err)
	}
}
