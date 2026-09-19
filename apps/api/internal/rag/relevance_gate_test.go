package rag

import (
	"context"
	"fmt"
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
	if result.Info.Version != "heuristic-relevance-gate-v3" || result.Info.ConfigVersion != "heuristic-relevance-hardened-v2" || result.Info.MinimumEvidenceCoverage != 0.25 {
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
		MatchedTerms:     []string{"runbook"}, EvidenceCoverage: 0.25,
	}, false, domain.RerankerInfo{Algorithm: heuristicRerankerAlgorithm}, DefaultHeuristicRelevanceGateConfig())
	if confidence != "medium" {
		t.Fatalf("expected pre-diversity score 0.59 to preserve medium confidence, got %q", confidence)
	}
}

func TestHeuristicRelevanceGateRejectsWeakTermOverlapDespiteVectorScore(t *testing.T) {
	t.Parallel()

	gate := NewHeuristicRelevanceGate(DefaultHeuristicRelevanceGateConfig())
	result, err := gate.Evaluate(context.Background(), RelevanceGateRequest{
		Query:    "lunar capacitor recovery procedure ZZ-0000",
		Reranker: domain.RerankerInfo{Algorithm: heuristicRerankerAlgorithm},
		Candidates: []domain.RetrievedDocumentChunk{{
			Chunk:      domain.DocumentChunk{ID: "chunk-1", Content: "The approved procedure handles tenant keys."},
			Similarity: 0.91, RerankScore: 0.91,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 0 {
		t.Fatalf("weak lexical evidence bypassed calibrated gate: %#v", result.Items)
	}
}

func TestHeuristicRelevanceGateRejectsSaturatedLexicalScoreWithoutEvidence(t *testing.T) {
	t.Parallel()

	candidates := make([]domain.RetrievedDocumentChunk, 5)
	for index := range candidates {
		candidates[index] = domain.RetrievedDocumentChunk{
			Document:     domain.Document{ID: fmt.Sprintf("doc-%d", index)},
			Chunk:        domain.DocumentChunk{ID: fmt.Sprintf("chunk-%d", index), Content: "unrelated operational notes"},
			Similarity:   0.33 + float64(index)*0.02,
			LexicalRank:  index + 1,
			LexicalScore: 1,
			RerankRank:   index + 1,
		}
	}
	result, err := NewHeuristicRelevanceGate(DefaultHeuristicRelevanceGateConfig()).Evaluate(context.Background(), RelevanceGateRequest{
		Query: "What is the lunar capacitor recovery procedure for ZZ-0000?", Candidates: candidates,
		Reranker: domain.RerankerInfo{Algorithm: heuristicRerankerAlgorithm},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 0 || len(result.Decisions) != len(candidates) {
		t.Fatalf("saturated lexical scores bypassed the relevance gate: %#v", result)
	}
	for _, decision := range result.Decisions {
		if decision.Accepted || decision.Confidence != "low" || decision.EvidenceCoverage != 0 || decision.FilterReason == "" {
			t.Fatalf("unexpected hardened gate decision: %#v", decision)
		}
	}
}

func TestHeuristicRelevanceGatePreservesExactIdentifierRecall(t *testing.T) {
	t.Parallel()
	if got := matchedIdentifier("AUTH-7F31", domain.RetrievedDocumentChunk{Chunk: domain.DocumentChunk{Content: "AUTH-7F310"}}); got != "" {
		t.Fatalf("identifier substring must not count as an exact match: %q", got)
	}

	result, err := NewHeuristicRelevanceGate(DefaultHeuristicRelevanceGateConfig()).Evaluate(context.Background(), RelevanceGateRequest{
		Query: "AUTH-7F31 怎么解决",
		Candidates: []domain.RetrievedDocumentChunk{{
			Document: domain.Document{ID: "doc-auth"}, Chunk: domain.DocumentChunk{ID: "chunk-auth", Content: "AUTH-7F31 means the refresh token expired."},
			LexicalRank: 1, LexicalScore: 1, RerankRank: 1,
		}},
		Reranker: domain.RerankerInfo{Algorithm: heuristicRerankerAlgorithm},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 1 || len(result.Decisions) != 1 || result.Decisions[0].IdentifierMatch != "AUTH-7F31" || !result.Decisions[0].Accepted {
		t.Fatalf("exact identifier recall regressed: %#v", result)
	}
}

func TestHeuristicRelevanceGateHandlesEmptyCandidates(t *testing.T) {
	t.Parallel()

	result, err := NewHeuristicRelevanceGate(DefaultHeuristicRelevanceGateConfig()).Evaluate(context.Background(), RelevanceGateRequest{Query: "anything"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 0 || len(result.Decisions) != 0 || result.Info.Version == "" {
		t.Fatalf("unexpected empty-candidate policy: %#v", result)
	}
}

func TestQueryTermsRemoveEnglishStopWordsAndKeepCJKTerms(t *testing.T) {
	t.Parallel()

	terms := QueryTerms("What is the audit retention 审计保留?")
	joined := strings.Join(terms, ",")
	if strings.Contains(joined, "what") || strings.Contains(joined, "the") || !strings.Contains(joined, "audit") || !strings.Contains(joined, "审计") {
		t.Fatalf("unexpected calibrated query terms: %v", terms)
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
	decisions := []domain.RelevanceGateDecision{
		{DocumentID: "doc-1", ChunkID: "chunk-1", Confidence: "medium", FilterReason: "test policy", Accepted: true},
		{DocumentID: "doc-2", ChunkID: "chunk-2", Confidence: "medium", FilterReason: "test policy", Accepted: true},
	}

	reordered := RelevanceGateResult{Info: info, Items: []domain.RetrievedDocumentChunk{
		classified(input[1], 1), classified(input[0], 2),
	}, Decisions: decisions}
	if err := validateRelevanceGateResult(input, reordered); err == nil || !strings.Contains(err.Error(), "ordering") {
		t.Fatalf("expected reordered candidates to be rejected, got %v", err)
	}

	mutated := classified(input[0], 1)
	mutated.RerankScore = 0.1
	if err := validateRelevanceGateResult(input, RelevanceGateResult{Info: info, Items: []domain.RetrievedDocumentChunk{mutated}, Decisions: decisions}); err == nil || !strings.Contains(err.Error(), "modifies ranked") {
		t.Fatalf("expected ranked candidate mutation to be rejected, got %v", err)
	}
}
