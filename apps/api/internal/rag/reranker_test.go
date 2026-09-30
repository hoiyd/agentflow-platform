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
	items := make([]RerankDecision, 0, len(request.Candidates))
	for index, candidate := range request.Candidates {
		decision := RerankDecision{DocumentID: candidate.Document.ID, ChunkID: candidate.Chunk.ID, RerankRank: index + 1, RerankScore: 0.9 - float64(index)*0.1}
		if index < len(s.scores) {
			decision.RerankScore = s.scores[index]
		}
		items = append(items, decision)
	}
	return RerankResult{Decisions: items, Info: s.info}, nil
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

func TestHeuristicRerankerDoesNotMutateCandidates(t *testing.T) {
	t.Parallel()

	candidates := []domain.RetrievedDocumentChunk{{
		Document: domain.Document{ID: "doc-1"},
		Chunk:    domain.DocumentChunk{ID: "chunk-1", Content: "deployment runbook"},
		RRFScore: reciprocalRank(1),
	}}
	result, err := NewHeuristicReranker(DefaultHeuristicRerankerConfig()).Rerank(context.Background(), RerankRequest{
		Query: "deployment", Candidates: candidates, Limit: 1,
	})
	if err != nil {
		t.Fatalf("rerank: %v", err)
	}
	if candidates[0].RerankRank != 0 || candidates[0].RerankScore != 0 || len(candidates[0].MatchedTerms) != 0 {
		t.Fatalf("expected input candidates to remain unchanged, got %#v", candidates[0])
	}
	if len(result.Decisions) != 1 || result.Decisions[0].RerankRank != 1 || result.Decisions[0].RerankScore <= 0 {
		t.Fatalf("expected independently ranked output, got %#v", result.Decisions)
	}
}

func TestDocumentDiversityPenaltyAffectsTopKSelection(t *testing.T) {
	t.Parallel()

	candidates := []RerankDecision{
		{DocumentID: "doc-a", ChunkID: "a1", RerankScore: 1.0},
		{DocumentID: "doc-a", ChunkID: "a2", RerankScore: 0.95},
		{DocumentID: "doc-b", ChunkID: "b1", RerankScore: 0.94},
	}

	topTwo := selectWithDocumentDiversity(candidates, 2)
	if len(topTwo) != 2 || topTwo[0].ChunkID != "a1" || topTwo[1].ChunkID != "b1" {
		t.Fatalf("expected the alternate document to replace the penalized duplicate, got %#v", topTwo)
	}

	all := selectWithDocumentDiversity(candidates, 3)
	if len(all) != 3 || all[0].ChunkID != "a1" || all[1].ChunkID != "b1" || all[2].ChunkID != "a2" {
		t.Fatalf("expected diversity-adjusted ordering, got %#v", all)
	}
	if all[2].DiversityPenalty != 0.04 || math.Abs(all[2].RerankScore-0.91) > 1e-9 {
		t.Fatalf("expected a2 to retain its effective diversity score, got %#v", all[2])
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
	if response.RelevanceGate.Version != "heuristic-relevance-gate-v3" || response.RelevanceGate.ConfigVersion != "heuristic-relevance-hardened-v2" {
		t.Fatalf("expected versioned relevance gate metadata, got %#v", response.RelevanceGate)
	}
}

func TestScoreOnlyCrossEncoderCannotBypassRelevanceGate(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name       string
		score      float64
		content    string
		wantItems  int
		confidence string
	}{
		{name: "low score is filtered", score: 0.2, content: "unrelated content", wantItems: 0},
		{name: "high score without evidence is filtered", score: 0.9, content: "generic content", wantItems: 0},
		{name: "high score with evidence is accepted", score: 0.9, content: "target runbook", wantItems: 1, confidence: "high"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			store := &retrievalStoreStub{denseItems: []domain.RetrievedDocumentChunk{{
				Document:   domain.Document{ID: "doc-1", Title: "Runbook"},
				Chunk:      domain.DocumentChunk{ID: "chunk-1", Content: testCase.content},
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

			response, err := pipeline.Search(context.Background(), domain.DocumentSearch{Query: "target", Limit: 1}, 1, Embedding{Vector: []float64{1}})
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

func TestRerankDecisionsRejectMalformedOutput(t *testing.T) {
	candidate := domain.RetrievedDocumentChunk{Document: domain.Document{ID: "doc-1"}, Chunk: domain.DocumentChunk{ID: "chunk-1"}}
	info := domain.RerankerInfo{Algorithm: "cross_encoder", Version: "v1", ConfigVersion: "config-v1", Provider: "test", Model: "model"}
	valid := RerankDecision{DocumentID: "doc-1", ChunkID: "chunk-1", RerankRank: 1, RerankScore: 0.8}
	for _, tc := range []struct {
		name, match string
		change      func(*RerankDecision, *domain.RerankerInfo)
	}{
		{"empty info", "reranker info", func(d *RerankDecision, i *domain.RerankerInfo) { *i = domain.RerankerInfo{} }},
		{"missing rank", "rerank_rank", func(d *RerankDecision, i *domain.RerankerInfo) { d.RerankRank = 0 }},
		{"non finite score", "non-finite", func(d *RerankDecision, i *domain.RerankerInfo) { d.RerankScore = math.NaN() }},
		{"unknown chunk", "unknown candidate", func(d *RerankDecision, i *domain.RerankerInfo) { d.ChunkID = "foreign" }},
		{"cross document", "unknown candidate", func(d *RerankDecision, i *domain.RerankerInfo) { d.DocumentID = "foreign" }},
		{"missing identity", "missing", func(d *RerankDecision, i *domain.RerankerInfo) { d.ChunkID = "" }},
		{"out of normalized range", "normalized", func(d *RerankDecision, i *domain.RerankerInfo) { d.RerankScore = 1.2 }},
		{"invalid coverage", "coverage", func(d *RerankDecision, i *domain.RerankerInfo) { d.EvidenceCoverage = 2 }},
		{"non finite evidence", "ranking evidence", func(d *RerankDecision, i *domain.RerankerInfo) { d.EvidenceScore = math.Inf(1) }},
		{"negative boost", "ranking evidence", func(d *RerankDecision, i *domain.RerankerInfo) { d.MetadataBoost = -1 }},
		{"provider without model", "together", func(d *RerankDecision, i *domain.RerankerInfo) { i.Model = "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			decision, metadata := valid, info
			tc.change(&decision, &metadata)
			if _, err := applyRerankResult(RerankRequest{Candidates: []domain.RetrievedDocumentChunk{candidate}, Limit: 1}, RerankResult{Decisions: []RerankDecision{decision}, Info: metadata}); err == nil || !strings.Contains(err.Error(), tc.match) {
				t.Fatalf("expected %s, got %v", tc.match, err)
			}
		})
	}
}

func TestRerankDecisionsRequireCompleteUniqueOrderedTopK(t *testing.T) {
	input := []domain.RetrievedDocumentChunk{{Document: domain.Document{ID: "d1"}, Chunk: domain.DocumentChunk{ID: "c1"}}, {Document: domain.Document{ID: "d2"}, Chunk: domain.DocumentChunk{ID: "c2"}}}
	first := RerankDecision{DocumentID: "d1", ChunkID: "c1", RerankRank: 1, RerankScore: 0.8}
	second := RerankDecision{DocumentID: "d2", ChunkID: "c2", RerankRank: 2, RerankScore: 0.7}
	duplicate := first
	duplicate.RerankRank = 2
	outOfOrder := second
	outOfOrder.RerankScore = 0.9
	for _, tc := range []struct {
		name, match string
		decisions   []RerankDecision
	}{
		{"missing", "complete top-k", []RerankDecision{first}},
		{"duplicate", "duplicates", []RerankDecision{first, duplicate}},
		{"order", "descending", []RerankDecision{first, outOfOrder}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := applyRerankResult(RerankRequest{Candidates: input, Limit: 2}, RerankResult{Decisions: tc.decisions, Info: domain.RerankerInfo{Algorithm: "cross_encoder", Version: "v1", ConfigVersion: "v1"}})
			if err == nil || !strings.Contains(err.Error(), tc.match) {
				t.Fatalf("expected %s, got %v", tc.match, err)
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
