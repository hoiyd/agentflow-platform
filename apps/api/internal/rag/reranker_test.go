package rag

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
)

type rerankerStub struct {
	request      RerankRequest
	contextValue string
	info         domain.RerankerInfo
	scores       []float64
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
		if index < len(s.scores) {
			items[index].RerankScore = s.scores[index]
		}
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
	if response.Items[0].Confidence != "high" || response.Items[0].FilterReason == "" {
		t.Fatalf("expected the relevance gate to classify score-only output, got %#v", response.Items[0])
	}
	if response.RelevanceGate.Version != "heuristic-relevance-gate-v1" || response.RelevanceGate.ConfigVersion != "heuristic-relevance-default-v1" {
		t.Fatalf("expected versioned relevance gate metadata, got %#v", response.RelevanceGate)
	}
}

func TestScoreOnlyCrossEncoderCannotBypassRelevanceGate(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name       string
		score      float64
		wantItems  int
		confidence string
	}{
		{name: "low score is filtered", score: 0.2, wantItems: 0},
		{name: "high score is accepted", score: 0.9, wantItems: 1, confidence: "high"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			store := &retrievalStoreStub{denseItems: []domain.RetrievedDocumentChunk{{
				Document:   domain.Document{ID: "doc-1", Title: "Runbook"},
				Chunk:      domain.DocumentChunk{ID: "chunk-1", Content: "generic content"},
				Similarity: 0.1,
				Score:      0.1,
			}}}
			stub := &rerankerStub{
				info: domain.RerankerInfo{
					Algorithm: "cross_encoder", Version: "cross-encoder-v1", ConfigVersion: "model-config-v1",
					Provider: "test-provider", Model: "test-model",
				},
				scores: []float64{testCase.score},
			}
			pipeline := NewRetrievalPipelineWithReranker(store, stub)

			response, err := pipeline.Search(context.Background(), domain.DocumentSearch{Query: "unrelated", Limit: 1}, 1, Embedding{Vector: []float64{1}})
			if err != nil {
				t.Fatalf("search score-only reranker: %v", err)
			}
			if len(response.Items) != testCase.wantItems {
				t.Fatalf("expected %d gated items, got %#v", testCase.wantItems, response.Items)
			}
			if testCase.wantItems == 0 {
				if !response.NoMatch {
					t.Fatalf("expected low score to produce no_match, got %#v", response)
				}
				return
			}
			if response.Items[0].Confidence != testCase.confidence || response.Items[0].FilterReason == "" {
				t.Fatalf("expected Gate-owned confidence, got %#v", response.Items[0])
			}
		})
	}
}

func TestValidateRerankResultRejectsMalformedOutput(t *testing.T) {
	t.Parallel()

	candidate := domain.RetrievedDocumentChunk{
		Document: domain.Document{ID: "doc-1"},
		Chunk:    domain.DocumentChunk{ID: "chunk-1"},
	}
	request := RerankRequest{Candidates: []domain.RetrievedDocumentChunk{candidate}, Limit: 1}
	validInfo := domain.RerankerInfo{Algorithm: "cross_encoder", Version: "v1", ConfigVersion: "config-v1", Provider: "test", Model: "model"}
	validItem := candidate
	validItem.RerankRank = 1
	validItem.RerankScore = 0.8

	testCases := []struct {
		name   string
		result RerankResult
		match  string
	}{
		{name: "empty info", result: RerankResult{Items: []domain.RetrievedDocumentChunk{validItem}}, match: "reranker info"},
		{name: "missing rank", result: RerankResult{Info: validInfo, Items: []domain.RetrievedDocumentChunk{candidate}}, match: "rerank_rank"},
		{name: "non finite score", result: RerankResult{Info: validInfo, Items: []domain.RetrievedDocumentChunk{{Document: validItem.Document, Chunk: validItem.Chunk, RerankRank: 1, RerankScore: math.NaN()}}}, match: "non-finite"},
		{name: "unknown chunk", result: RerankResult{Info: validInfo, Items: []domain.RetrievedDocumentChunk{{Document: domain.Document{ID: "doc-2"}, Chunk: domain.DocumentChunk{ID: "chunk-2"}, RerankRank: 1, RerankScore: 0.8}}}, match: "unknown candidate"},
		{name: "out of normalized range", result: RerankResult{Info: validInfo, Items: []domain.RetrievedDocumentChunk{{Document: validItem.Document, Chunk: validItem.Chunk, RerankRank: 1, RerankScore: 1.2}}}, match: "normalized"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			err := validateRerankResult(request, testCase.result)
			if err == nil || !strings.Contains(err.Error(), testCase.match) {
				t.Fatalf("expected error containing %q, got %v", testCase.match, err)
			}
		})
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
