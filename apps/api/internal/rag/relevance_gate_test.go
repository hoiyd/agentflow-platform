package rag

import (
	"context"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
)

func TestHeuristicRelevanceGateOwnsConfidence(t *testing.T) {
	t.Parallel()

	gate := NewHeuristicRelevanceGate(DefaultHeuristicRelevanceGateConfig())
	result, err := gate.Evaluate(context.Background(), RelevanceGateRequest{
		Reranker: domain.RerankerInfo{Algorithm: "cross_encoder", Version: "v1", ConfigVersion: "config-v1"},
		Candidates: []domain.RetrievedDocumentChunk{{
			Document:     domain.Document{ID: "doc-1"},
			Chunk:        domain.DocumentChunk{ID: "chunk-1"},
			Similarity:   0.1,
			RerankRank:   1,
			RerankScore:  0.2,
			Confidence:   "high",
			FilterReason: "untrusted reranker decision",
		}},
	})
	if err != nil {
		t.Fatalf("evaluate relevance gate: %v", err)
	}
	if len(result.Items) != 0 {
		t.Fatalf("expected Gate to overwrite and reject reranker confidence, got %#v", result.Items)
	}
	if result.Info.Version != "heuristic-relevance-gate-v1" || result.Info.ConfigVersion != "heuristic-relevance-default-v1" {
		t.Fatalf("unexpected relevance gate metadata: %#v", result.Info)
	}
}

func TestValidateRelevanceGateResultRequiresClassifiedOutput(t *testing.T) {
	t.Parallel()

	input := []domain.RetrievedDocumentChunk{{Document: domain.Document{ID: "doc-1"}, Chunk: domain.DocumentChunk{ID: "chunk-1"}}}
	result := RelevanceGateResult{
		Info:  domain.RelevanceGateInfo{Policy: "test", Version: "v1", ConfigVersion: "config-v1"},
		Items: []domain.RetrievedDocumentChunk{{Document: input[0].Document, Chunk: input[0].Chunk, RerankRank: 1}},
	}
	if err := validateRelevanceGateResult(input, result); err == nil {
		t.Fatal("expected missing Gate confidence and reason to be rejected")
	}
}
