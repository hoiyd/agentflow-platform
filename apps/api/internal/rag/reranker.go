package rag

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"agentflow-platform/apps/api/internal/domain"
)

// RerankRequest is provider-neutral so deterministic and model-backed
// rerankers can share the same RetrievalPipeline contract.
type RerankRequest struct {
	Query      string
	Candidates []domain.RetrievedDocumentChunk
	Limit      int
}

// RerankDecision contains ranking-owned fields only. The Pipeline retains
// document content, scope, index identity and recall evidence.
type RerankDecision struct {
	DocumentID       string
	ChunkID          string
	RerankRank       int
	LexicalBoost     float64
	MetadataBoost    float64
	DiversityPenalty float64
	RerankScore      float64
	MatchedTerms     []string
	EvidenceScore    float64
	EvidenceCoverage float64
}

type RerankResult struct {
	Decisions []RerankDecision
	Info      domain.RerankerInfo
}

// Reranker orders fused recall candidates. Implementations must return ranking
// evidence and the identity/configuration actually used for this request.
type Reranker interface {
	Rerank(context.Context, RerankRequest) (RerankResult, error)
}

// applyRerankResult validates identities and writes only ranking-owned values.
func applyRerankResult(request RerankRequest, result RerankResult) ([]domain.RetrievedDocumentChunk, error) {
	if err := validateRerankerInfo(result.Info); err != nil {
		return nil, err
	}
	if request.Limit > 0 {
		expected := minInt(request.Limit, len(request.Candidates))
		if len(result.Decisions) != expected {
			return nil, fmt.Errorf("returned %d items; expected complete top-k of %d", len(result.Decisions), expected)
		}
	}
	candidates := make(map[string]domain.RetrievedDocumentChunk, len(request.Candidates))
	for _, candidate := range request.Candidates {
		if strings.TrimSpace(candidate.Document.ID) == "" || strings.TrimSpace(candidate.Chunk.ID) == "" {
			return nil, errors.New("candidate is missing document_id or chunk_id")
		}
		if _, duplicate := candidates[candidate.Chunk.ID]; duplicate {
			return nil, fmt.Errorf("duplicate input candidate %q", candidate.Chunk.ID)
		}
		candidates[candidate.Chunk.ID] = candidate
	}
	items := make([]domain.RetrievedDocumentChunk, 0, len(result.Decisions))
	seen := make(map[string]bool, len(result.Decisions))
	for index, decision := range result.Decisions {
		if strings.TrimSpace(decision.DocumentID) == "" || strings.TrimSpace(decision.ChunkID) == "" {
			return nil, fmt.Errorf("item %d is missing document_id or chunk_id", index)
		}
		candidate, ok := candidates[decision.ChunkID]
		if !ok || candidate.Document.ID != decision.DocumentID {
			return nil, fmt.Errorf("item %d references unknown candidate %q", index, decision.ChunkID)
		}
		if seen[decision.ChunkID] {
			return nil, fmt.Errorf("item %d duplicates chunk %q", index, decision.ChunkID)
		}
		seen[decision.ChunkID] = true
		if decision.RerankRank != index+1 {
			return nil, fmt.Errorf("item %d has rerank_rank %d; expected %d", index, decision.RerankRank, index+1)
		}
		if !finiteScore(decision.RerankScore) {
			return nil, fmt.Errorf("item %d has a non-finite rerank_score", index)
		}
		if result.Info.Algorithm != heuristicRerankerAlgorithm && (decision.RerankScore < 0 || decision.RerankScore > 1) {
			return nil, fmt.Errorf("item %d has rerank_score outside the normalized [0,1] range", index)
		}
		if index > 0 && result.Decisions[index-1].RerankScore < decision.RerankScore {
			return nil, fmt.Errorf("item %d is out of descending rerank_score order", index)
		}
		for _, score := range []float64{decision.LexicalBoost, decision.MetadataBoost, decision.DiversityPenalty, decision.EvidenceScore, decision.EvidenceCoverage} {
			if !finiteScore(score) || score < 0 {
				return nil, fmt.Errorf("item %d has invalid ranking evidence", index)
			}
		}
		if decision.EvidenceCoverage > 1 {
			return nil, fmt.Errorf("item %d has invalid evidence coverage", index)
		}
		candidate.RerankRank = decision.RerankRank
		candidate.LexicalBoost = decision.LexicalBoost
		candidate.MetadataBoost = decision.MetadataBoost
		candidate.DiversityPenalty = decision.DiversityPenalty
		candidate.RerankScore = decision.RerankScore
		candidate.MatchedTerms = append([]string(nil), decision.MatchedTerms...)
		candidate.EvidenceScore = decision.EvidenceScore
		candidate.EvidenceCoverage = decision.EvidenceCoverage
		candidate.Confidence, candidate.FilterReason = "", ""
		items = append(items, candidate)
	}
	return items, nil
}

func validateRerankerInfo(info domain.RerankerInfo) error {
	if strings.TrimSpace(info.Algorithm) == "" || strings.TrimSpace(info.Version) == "" || strings.TrimSpace(info.ConfigVersion) == "" {
		return errors.New("reranker info requires algorithm, version, and config_version")
	}
	if (strings.TrimSpace(info.Provider) == "") != (strings.TrimSpace(info.Model) == "") {
		return errors.New("reranker provider and model must be supplied together")
	}
	return nil
}
