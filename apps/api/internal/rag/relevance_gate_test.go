package rag

import (
	"context"
	"strings"
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

func TestHeuristicRelevanceGateRecomputesEvidence(t *testing.T) {
	t.Parallel()

	gate := NewHeuristicRelevanceGate(DefaultHeuristicRelevanceGateConfig())
	result, err := gate.Evaluate(context.Background(), RelevanceGateRequest{
		Query:    "missing evidence",
		Reranker: domain.RerankerInfo{Algorithm: "cross_encoder", Version: "v1", ConfigVersion: "config-v1"},
		Candidates: []domain.RetrievedDocumentChunk{{
			Document:         domain.Document{ID: "doc-1"},
			Chunk:            domain.DocumentChunk{ID: "chunk-1", Content: "unrelated content"},
			Similarity:       0.1,
			RerankRank:       1,
			RerankScore:      0.2,
			MatchedTerms:     []string{"missing evidence"},
			EvidenceCoverage: 1,
			EvidenceScore:    1,
		}},
	})
	if err != nil {
		t.Fatalf("evaluate relevance gate: %v", err)
	}
	if len(result.Items) != 0 {
		t.Fatalf("expected fabricated reranker evidence to be ignored, got %#v", result.Items)
	}
}

func TestHeuristicConfidenceIgnoresPostSelectionDiversityPenalty(t *testing.T) {
	t.Parallel()

	confidence, _ := relevanceConfidence(domain.RetrievedDocumentChunk{
		RerankScore:      0.55,
		DiversityPenalty: 0.04,
		MatchedTerms:     []string{"runbook"},
	}, domain.RerankerInfo{Algorithm: heuristicRerankerAlgorithm})
	if confidence != "medium" {
		t.Fatalf("expected pre-diversity score 0.59 to preserve medium confidence, got %q", confidence)
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

func TestValidateRelevanceGateResultRejectsMutationAndReordering(t *testing.T) {
	t.Parallel()

	input := []domain.RetrievedDocumentChunk{
		{Document: domain.Document{ID: "doc-1"}, Chunk: domain.DocumentChunk{ID: "chunk-1"}, RerankRank: 1, RerankScore: 0.9},
		{Document: domain.Document{ID: "doc-2"}, Chunk: domain.DocumentChunk{ID: "chunk-2"}, RerankRank: 2, RerankScore: 0.8},
	}
	classified := func(item domain.RetrievedDocumentChunk, rank int) domain.RetrievedDocumentChunk {
		item.RerankRank = rank
		item.Confidence = "medium"
		item.FilterReason = "test policy"
		return item
	}
	info := domain.RelevanceGateInfo{Policy: "test", Version: "v1", ConfigVersion: "config-v1"}

	reordered := RelevanceGateResult{Info: info, Items: []domain.RetrievedDocumentChunk{
		classified(input[1], 1), classified(input[0], 2),
	}}
	if err := validateRelevanceGateResult(input, reordered); err == nil || !strings.Contains(err.Error(), "ordering") {
		t.Fatalf("expected reordered candidates to be rejected, got %v", err)
	}

	mutated := classified(input[0], 1)
	mutated.RerankScore = 0.1
	if err := validateRelevanceGateResult(input, RelevanceGateResult{Info: info, Items: []domain.RetrievedDocumentChunk{mutated}}); err == nil || !strings.Contains(err.Error(), "modifies ranked") {
		t.Fatalf("expected ranked candidate mutation to be rejected, got %v", err)
	}
}
