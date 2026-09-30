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
	if hasAcceptedDecision(result.Decisions) {
		t.Fatalf("expected Gate to overwrite and reject reranker confidence, got %#v", result.Decisions)
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
	if hasAcceptedDecision(result.Decisions) {
		t.Fatalf("expected fabricated reranker evidence to be ignored, got %#v", result.Decisions)
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
	if hasAcceptedDecision(result.Decisions) {
		t.Fatalf("weak lexical evidence bypassed calibrated gate: %#v", result.Decisions)
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
	if hasAcceptedDecision(result.Decisions) || len(result.Decisions) != len(candidates) {
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
	if len(result.Decisions) != 1 || result.Decisions[0].IdentifierMatch != "AUTH-7F31" || !result.Decisions[0].Accepted {
		t.Fatalf("exact identifier recall regressed: %#v", result)
	}
}

func TestHeuristicRelevanceGateHandlesEmptyCandidates(t *testing.T) {
	t.Parallel()

	result, err := NewHeuristicRelevanceGate(DefaultHeuristicRelevanceGateConfig()).Evaluate(context.Background(), RelevanceGateRequest{Query: "anything"})
	if err != nil {
		t.Fatal(err)
	}
	if hasAcceptedDecision(result.Decisions) || len(result.Decisions) != 0 || result.Info.Version == "" {
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

func hasAcceptedDecision(decisions []RelevanceDecision) bool {
	for _, decision := range decisions {
		if decision.Accepted {
			return true
		}
	}
	return false
}

func TestRelevanceDecisionsRejectMalformedOutput(t *testing.T) {
	input := []domain.RetrievedDocumentChunk{{Document: domain.Document{ID: "d1"}, Chunk: domain.DocumentChunk{ID: "c1"}}, {Document: domain.Document{ID: "d2"}, Chunk: domain.DocumentChunk{ID: "c2"}}}
	valid := []RelevanceDecision{{DocumentID: "d1", ChunkID: "c1", Confidence: "high", FilterReason: "evidence", Accepted: true}, {DocumentID: "d2", ChunkID: "c2", Confidence: "low", FilterReason: "no evidence"}}
	info := domain.RelevanceGateInfo{Policy: "test", Version: "v1", ConfigVersion: "v1"}
	for _, tc := range []struct {
		name, match string
		change      func(*RelevanceGateResult)
	}{
		{"missing info", "gate info", func(r *RelevanceGateResult) { r.Info = domain.RelevanceGateInfo{} }},
		{"missing decision", "one for each", func(r *RelevanceGateResult) { r.Decisions = r.Decisions[:1] }},
		{"unknown", "does not match", func(r *RelevanceGateResult) { r.Decisions[0].ChunkID = "foreign" }},
		{"cross document", "does not match", func(r *RelevanceGateResult) { r.Decisions[0].DocumentID = "foreign" }},
		{"duplicate", "does not match", func(r *RelevanceGateResult) { r.Decisions[1] = r.Decisions[0] }},
		{"reordered", "ordering", func(r *RelevanceGateResult) { r.Decisions[0], r.Decisions[1] = r.Decisions[1], r.Decisions[0] }},
		{"unclassified", "invalid confidence", func(r *RelevanceGateResult) { r.Decisions[0].Confidence = "" }},
		{"missing reason", "filter_reason", func(r *RelevanceGateResult) { r.Decisions[0].FilterReason = " " }},
		{"inconsistent acceptance", "inconsistent", func(r *RelevanceGateResult) { r.Decisions[0].Accepted = false }},
		{"invalid coverage", "invalid evidence", func(r *RelevanceGateResult) { r.Decisions[0].EvidenceCoverage = 2 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := RelevanceGateResult{Info: info, Decisions: append([]RelevanceDecision(nil), valid...)}
			tc.change(&result)
			if _, err := applyRelevanceGateResult(input, result); err == nil || !strings.Contains(err.Error(), tc.match) {
				t.Fatalf("expected %s, got %v", tc.match, err)
			}
		})
	}
}
