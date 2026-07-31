package rag

import (
	"context"
	"errors"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
)

type rerankerStub struct {
	request      RerankRequest
	contextValue string
	info         domain.RerankerInfo
	err          error
}

func (s *rerankerStub) Rerank(ctx context.Context, request RerankRequest) (RerankResult, error) {
	s.request = request
	s.contextValue, _ = ctx.Value(rerankerContextKey{}).(string)
	if s.err != nil {
		return RerankResult{}, s.err
	}
	items := append([]domain.RetrievedDocumentChunk(nil), request.Candidates...)
	for index := range items {
		items[index].RerankRank = index + 1
		items[index].RerankScore = 0.9 - float64(index)*0.1
		items[index].Confidence = "high"
		items[index].FilterReason = "test reranker accepted candidate"
	}
	return RerankResult{Items: items, Info: s.info}, nil
}

type rerankerContextKey struct{}

func TestHeuristicRerankerExposesVersionedConfiguration(t *testing.T) {
	t.Parallel()

	reranker := NewHeuristicReranker(HeuristicRerankerConfig{ConfigVersion: "candidate-policy-7"})
	info := reranker.Info()
	if info.Algorithm != "heuristic" || info.Version != "heuristic-reranker-v1" || info.ConfigVersion != "candidate-policy-7" {
		t.Fatalf("unexpected reranker metadata: %#v", info)
	}
}

func TestRetrievalPipelineUsesInjectedReranker(t *testing.T) {
	t.Parallel()

	store := &retrievalStoreStub{denseItems: []domain.RetrievedDocumentChunk{{
		Document:   domain.Document{ID: "doc-1", Title: "Runbook"},
		Chunk:      domain.DocumentChunk{ID: "chunk-1", Content: "AUTH-7F31 recovery"},
		Similarity: 0.8,
		Score:      0.8,
	}}}
	stub := &rerankerStub{info: domain.RerankerInfo{
		Algorithm:     "cross_encoder",
		Version:       "cross-encoder-adapter-v1",
		ConfigVersion: "support-reranker-v3",
		Provider:      "test-provider",
		Model:         "test-model",
	}}
	pipeline := NewRetrievalPipelineWithReranker(store, stub)
	ctx := context.WithValue(context.Background(), rerankerContextKey{}, "request-42")

	response, err := pipeline.Search(ctx, domain.DocumentSearch{Query: "AUTH-7F31", Limit: 1}, 1, Embedding{
		Vector: []float64{1}, Provider: "test", Model: "embedding-v1", Dimensions: 1,
	})
	if err != nil {
		t.Fatalf("search with injected reranker: %v", err)
	}
	if stub.contextValue != "request-42" || stub.request.Query != "AUTH-7F31" || stub.request.Limit != 1 {
		t.Fatalf("expected context and request to reach reranker, got context=%q request=%#v", stub.contextValue, stub.request)
	}
	if len(stub.request.Candidates) != 1 || stub.request.Candidates[0].FusionRank != 1 {
		t.Fatalf("expected fused candidates to reach reranker, got %#v", stub.request.Candidates)
	}
	if response.Reranker != stub.info || len(response.Items) != 1 || response.Items[0].RerankRank != 1 {
		t.Fatalf("expected injected reranker output and metadata, got %#v", response)
	}
}

func TestRetrievalPipelinePropagatesRerankerFailure(t *testing.T) {
	t.Parallel()

	want := errors.New("cross-encoder unavailable")
	store := &retrievalStoreStub{denseItems: []domain.RetrievedDocumentChunk{{
		Document: domain.Document{ID: "doc-1"},
		Chunk:    domain.DocumentChunk{ID: "chunk-1", Content: "runbook"},
	}}}
	pipeline := NewRetrievalPipelineWithReranker(store, &rerankerStub{err: want})

	_, err := pipeline.Search(context.Background(), domain.DocumentSearch{Query: "runbook", Limit: 1}, 1, Embedding{Vector: []float64{1}})
	if !errors.Is(err, want) {
		t.Fatalf("expected wrapped reranker error, got %v", err)
	}
}
