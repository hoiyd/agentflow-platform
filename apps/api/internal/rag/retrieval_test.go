package rag

import (
	"context"
	"strings"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
)

type retrievalStoreStub struct {
	denseSearch   domain.DocumentSearch
	lexicalSearch domain.DocumentSearch
	denseItems    []domain.RetrievedDocumentChunk
	lexicalItems  []domain.RetrievedDocumentChunk
}

func (s *retrievalStoreStub) SearchDocumentChunks(search domain.DocumentSearch) ([]domain.RetrievedDocumentChunk, error) {
	s.denseSearch = search
	return append([]domain.RetrievedDocumentChunk(nil), s.denseItems...), nil

}

func (s *retrievalStoreStub) SearchDocumentChunksLexical(search domain.DocumentSearch) ([]domain.RetrievedDocumentChunk, error) {
	s.lexicalSearch = search
	return append([]domain.RetrievedDocumentChunk(nil), s.lexicalItems...), nil
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
	store := &retrievalStoreStub{denseItems: []domain.RetrievedDocumentChunk{
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
	if store.denseSearch.Limit != CandidateLimit(3) || store.lexicalSearch.Limit != CandidateLimit(3) {
		t.Fatalf("expected both recalls to use candidate limit %d, got dense=%d lexical=%d", CandidateLimit(3), store.denseSearch.Limit, store.lexicalSearch.Limit)
	}
	if store.denseSearch.EmbeddingProvider != "test" || store.denseSearch.EmbeddingModel != "embedding-v1" || len(store.denseSearch.Embedding) != 3 {
		t.Fatalf("expected pipeline to prepare embedded recall request, got %#v", store.denseSearch)
	}
	if len(store.lexicalSearch.LexicalTerms) == 0 {
		t.Fatalf("expected pipeline to prepare lexical terms, got %#v", store.lexicalSearch)
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

func TestRetrievalPipelineRecallsIdentifierOutsideDenseCandidates(t *testing.T) {
	store := &retrievalStoreStub{
		denseItems: []domain.RetrievedDocumentChunk{{
			Document:   domain.Document{ID: "doc_generic", Title: "Authentication guide"},
			Chunk:      domain.DocumentChunk{ID: "chunk_generic", Content: "General authentication troubleshooting."},
			Similarity: 0.82, Score: 0.82,
		}},
		lexicalItems: []domain.RetrievedDocumentChunk{{
			Document:     domain.Document{ID: "doc_error", Title: "Error catalog"},
			Chunk:        domain.DocumentChunk{ID: "chunk_error", Content: "AUTH-7F31 means the refresh token has expired."},
			RecencyBoost: 0.03, Score: 1.03, LexicalScore: 1,
		}},
	}
	pipeline := NewRetrievalPipeline(store)

	response, err := pipeline.Search(domain.DocumentSearch{Query: "AUTH-7F31 怎么解决", Limit: 3}, 3, Embedding{
		Vector: []float64{1, 0, 0}, Provider: "test", Model: "embedding-v1", Dimensions: 3,
	})
	if err != nil {
		t.Fatalf("search pipeline: %v", err)
	}
	if len(response.Items) == 0 || response.Items[0].Chunk.ID != "chunk_error" {
		t.Fatalf("expected lexical-only identifier result to lead the reranked candidates, got %#v", response.Items)
	}
	if response.Items[0].VectorRank != 0 || response.Items[0].LexicalRank != 1 || response.Items[0].LexicalScore != 1 {
		t.Fatalf("expected lexical-only rank evidence, got %#v", response.Items[0])
	}
}
