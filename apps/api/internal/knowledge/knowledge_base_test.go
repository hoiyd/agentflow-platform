package knowledge

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/openai"
	"agentflow-platform/apps/api/internal/rag"
	"agentflow-platform/apps/api/internal/store"
)

type embeddingStub struct {
	embedding openai.Embedding
	err       error
}

type knowledgeRetrieverStub struct {
	search    domain.DocumentSearch
	limit     int
	embedding rag.Embedding
}

func (s *knowledgeRetrieverStub) Search(_ context.Context, search domain.DocumentSearch, limit int, embedding rag.Embedding) (domain.DocumentSearchResponse, error) {
	s.search = search
	s.limit = limit
	s.embedding = embedding
	return domain.DocumentSearchResponse{NoMatch: true}, nil
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
	if len(response.Items) != 1 || response.Embedding.Provider != "test" || response.Fusion.Algorithm != "rrf" || response.Reranker.Algorithm != "heuristic" || response.Security.PolicyVersion != domain.RAGPromptGuardPolicyVersion {
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
	if evaluation.Fusion != response.Fusion {
		t.Fatalf("expected evaluation fusion metadata %#v, got %#v", response.Fusion, evaluation.Fusion)
	}
	if evaluation.Reranker != response.Reranker {
		t.Fatalf("expected evaluation reranker metadata %#v, got %#v", response.Reranker, evaluation.Reranker)
	}
	if evaluation.RelevanceGate != response.RelevanceGate || response.RelevanceGate.Version != "heuristic-relevance-gate-v1" {
		t.Fatalf("expected evaluation relevance gate metadata %#v, got %#v", response.RelevanceGate, evaluation.RelevanceGate)
	}
	if len(evaluation.Cases) != 1 || evaluation.Cases[0].Security.PolicyVersion != domain.RAGPromptGuardPolicyVersion {
		t.Fatalf("expected evaluation case security metadata, got %#v", evaluation.Cases)
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

func TestKnowledgeBaseDelegatesSearchToRetrievalPipeline(t *testing.T) {
	retriever := &knowledgeRetrieverStub{}
	knowledgeBase := NewKnowledgeBaseWithRetriever(nil, embeddingStub{embedding: openai.Embedding{
		Vector: []float64{1, 0, 0}, Provider: "test", Model: "embedding-v1", Dimensions: 3,
	}}, retriever)

	if _, err := knowledgeBase.Search(context.Background(), domain.DocumentSearch{Query: "launch password", Limit: 4}, 4); err != nil {
		t.Fatalf("search knowledge: %v", err)
	}
	if retriever.limit != 4 || retriever.search.Query != "launch password" {
		t.Fatalf("expected search request to be delegated, got search=%#v limit=%d", retriever.search, retriever.limit)
	}
	if retriever.embedding.Provider != "test" || retriever.embedding.Model != "embedding-v1" || retriever.embedding.Dimensions != 3 || len(retriever.embedding.Vector) != 3 {
		t.Fatalf("expected embedding metadata, got %#v", retriever.embedding)
	}
}

func TestKnowledgeBaseEvaluatesVersionedGoldenDataset(t *testing.T) {
	t.Parallel()

	unanswerable := false
	retriever := &knowledgeRetrieverStub{}
	knowledgeBase := NewKnowledgeBaseWithRetriever(nil, embeddingStub{embedding: openai.Embedding{
		Vector: []float64{1}, Provider: "test", Model: "embedding-v1", Dimensions: 1,
	}}, retriever)

	response, err := knowledgeBase.Evaluate(context.Background(), domain.RAGEvaluationRunRequest{
		Dataset: &domain.RAGGoldenDataset{
			SchemaVersion: domain.RAGGoldenDatasetSchemaVersion,
			ID:            "no-answer-baseline",
			Version:       "1.0.0",
			Cases: []domain.RAGEvaluationCase{{
				ID: "missing", Query: "unknown product", Answerable: &unanswerable,
				ForbiddenSources: []domain.RAGGoldenSource{{DocumentID: "doc-secret"}},
			}},
		},
	})
	if err != nil {
		t.Fatalf("evaluate golden dataset: %v", err)
	}
	if response.Dataset == nil || response.Dataset.ID != "no-answer-baseline" || response.Dataset.Version != "1.0.0" {
		t.Fatalf("expected dataset identity in response, got %#v", response.Dataset)
	}
	if response.Summary.Total != 1 || response.Summary.AnswerableCases != 0 || response.Summary.UnanswerableCases != 1 || response.Summary.Misses != 0 || len(response.Cases) != 1 || !response.Cases[0].Hit || response.Cases[0].Answerable {
		t.Fatalf("unexpected no-answer evaluation result: %#v", response)
	}
}
