package rag

import (
	"context"
	"strings"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
)

type retrievalStoreStub struct {
	search domain.DocumentSearch
	items  []domain.RetrievedDocumentChunk
}

func (s *retrievalStoreStub) SearchDocumentChunks(search domain.DocumentSearch) ([]domain.RetrievedDocumentChunk, error) {
	s.search = search
	return append([]domain.RetrievedDocumentChunk(nil), s.items...), nil
}

func TestEmbedQueryNormalizesInput(t *testing.T) {
	var embeddedQuery string
	embedding, err := EmbedQuery(context.Background(), "  "+strings.Repeat("x", 3200)+"  ", func(_ context.Context, query string) (Embedding, error) {
		embeddedQuery = query
		return Embedding{Vector: []float64{1}, Provider: "test", Model: "embedding-v1"}, nil
	})
	if err != nil {
		t.Fatalf("embed query: %v", err)
	}
	if len(embeddedQuery) != 3000 {
		t.Fatalf("expected normalized embedding query length 3000, got %d", len(embeddedQuery))
	}
	if len(embedding.Vector) != 1 || embedding.Provider != "test" {
		t.Fatalf("unexpected embedding: %#v", embedding)
	}
}

func TestRetrievalPipelineAppliesCandidateRecallRerankAndGate(t *testing.T) {
	store := &retrievalStoreStub{items: []domain.RetrievedDocumentChunk{
		{
			Document:   domain.Document{ID: "doc_unrelated", Title: "Dinner ideas"},
			Chunk:      domain.DocumentChunk{ID: "chunk_unrelated", Content: "A collection of unrelated dinner recipes."},
			Similarity: 0.10,
			Score:      0.10,
		},
		{
			Document:   domain.Document{ID: "doc_launch", Title: "Launch notes"},
			Chunk:      domain.DocumentChunk{ID: "chunk_launch", Content: "The launch password is amber-9137."},
			Similarity: 0.61,
			Score:      0.61,
		},
	}}
	pipeline := NewRetrievalPipeline(store)

	response, err := pipeline.Search(domain.DocumentSearch{
		Query: "launch password",
		Limit: 3,
	}, 3, Embedding{Vector: []float64{1, 0, 0}, Provider: "test", Model: "embedding-v1", Dimensions: 3})
	if err != nil {
		t.Fatalf("search pipeline: %v", err)
	}
	if store.search.Limit != CandidateLimit(3) {
		t.Fatalf("expected candidate limit %d, got %d", CandidateLimit(3), store.search.Limit)
	}
	if store.search.EmbeddingProvider != "test" || store.search.EmbeddingModel != "embedding-v1" || len(store.search.Embedding) != 3 {
		t.Fatalf("expected pipeline to prepare embedded recall request, got %#v", store.search)
	}
	if len(response.Items) != 1 || response.Items[0].Document.ID != "doc_launch" {
		t.Fatalf("expected only the relevant launch result, got %#v", response.Items)
	}
	if response.Items[0].VectorRank != 2 || response.Items[0].RerankRank != 1 {
		t.Fatalf("expected vector and rerank positions to be preserved, got %#v", response.Items[0])
	}
	if response.Items[0].Confidence == "" || response.Items[0].Confidence == "low" {
		t.Fatalf("expected relevant result to pass the gate, got %#v", response.Items[0])
	}
	if response.NoMatch {
		t.Fatal("expected a confident match")
	}
}
