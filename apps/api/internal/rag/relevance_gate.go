package rag

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"agentflow-platform/apps/api/internal/domain"
)

const (
	heuristicRelevanceGatePolicy      = "heuristic"
	heuristicRelevanceGateVersion     = "heuristic-relevance-gate-v3"
	defaultRelevanceGateConfigVersion = "heuristic-relevance-hardened-v2"
	defaultMinimumEvidenceCoverage    = 0.25
)

type RelevanceGateRequest struct {
	Query      string
	Candidates []domain.RetrievedDocumentChunk
	Reranker   domain.RerankerInfo
}

type RelevanceGateResult struct {
	Items     []domain.RetrievedDocumentChunk
	Decisions []domain.RelevanceGateDecision
	Info      domain.RelevanceGateInfo
}

// RelevanceGate owns confidence classification and filtering independently of
// candidate ranking. It must not trust Confidence supplied by a Reranker.
type RelevanceGate interface {
	Evaluate(context.Context, RelevanceGateRequest) (RelevanceGateResult, error)
}

type HeuristicRelevanceGateConfig struct {
	ConfigVersion           string
	MinimumEvidenceCoverage float64
}

func DefaultHeuristicRelevanceGateConfig() HeuristicRelevanceGateConfig {
	return HeuristicRelevanceGateConfig{
		ConfigVersion: defaultRelevanceGateConfigVersion, MinimumEvidenceCoverage: defaultMinimumEvidenceCoverage,
	}
}

type HeuristicRelevanceGate struct {
	config HeuristicRelevanceGateConfig
}

var _ RelevanceGate = (*HeuristicRelevanceGate)(nil)

func NewHeuristicRelevanceGate(config HeuristicRelevanceGateConfig) *HeuristicRelevanceGate {
	config.ConfigVersion = strings.TrimSpace(config.ConfigVersion)
	if config.ConfigVersion == "" {
		config.ConfigVersion = defaultRelevanceGateConfigVersion
	}
	if config.MinimumEvidenceCoverage <= 0 || config.MinimumEvidenceCoverage > 1 {
		config.MinimumEvidenceCoverage = defaultMinimumEvidenceCoverage
	}
	return &HeuristicRelevanceGate{config: config}
}

func (g *HeuristicRelevanceGate) Info() domain.RelevanceGateInfo {
	configVersion := defaultRelevanceGateConfigVersion
	if g != nil && strings.TrimSpace(g.config.ConfigVersion) != "" {
		configVersion = g.config.ConfigVersion
	}
	minimumCoverage := defaultMinimumEvidenceCoverage
	if g != nil && g.config.MinimumEvidenceCoverage > 0 && g.config.MinimumEvidenceCoverage <= 1 {
		minimumCoverage = g.config.MinimumEvidenceCoverage
	}
	return domain.RelevanceGateInfo{
		Policy:                  heuristicRelevanceGatePolicy,
		Version:                 heuristicRelevanceGateVersion,
		ConfigVersion:           configVersion,
		MinimumEvidenceCoverage: minimumCoverage,
	}
}

func (g *HeuristicRelevanceGate) Evaluate(_ context.Context, request RelevanceGateRequest) (RelevanceGateResult, error) {
	filtered := make([]domain.RetrievedDocumentChunk, 0, len(request.Candidates))
	decisions := make([]domain.RelevanceGateDecision, 0, len(request.Candidates))
	queryTerms := QueryTerms(request.Query)
	for _, item := range request.Candidates {
		item.MatchedTerms = matchedTerms(request.Query, queryTerms, item)
		item.EvidenceCoverage = evidenceCoverage(queryTerms, item.MatchedTerms)
		item.EvidenceScore = evidenceScore(request.Query, queryTerms, item)
		identifier := matchedIdentifier(request.Query, item)
		item.Confidence, item.FilterReason = relevanceConfidence(item, identifier != "", request.Reranker, g.config)
		decisions = append(decisions, domain.RelevanceGateDecision{
			DocumentID: item.Document.ID, ChunkID: item.Chunk.ID,
			LexicalRank: item.LexicalRank, LexicalScore: item.LexicalScore,
			Similarity: item.Similarity, RerankScore: item.RerankScore,
			MatchedTerms:  append([]string{}, item.MatchedTerms...),
			EvidenceScore: item.EvidenceScore, EvidenceCoverage: item.EvidenceCoverage,
			IdentifierMatch: identifier, Confidence: item.Confidence,
			FilterReason: item.FilterReason, Accepted: item.Confidence != "low",
		})
		if item.Confidence == "low" {
			continue
		}
		item.RerankRank = len(filtered) + 1
		filtered = append(filtered, item)
	}
	return RelevanceGateResult{Items: filtered, Decisions: decisions, Info: g.Info()}, nil
}

func validateRelevanceGateResult(input []domain.RetrievedDocumentChunk, result RelevanceGateResult) error {
	if strings.TrimSpace(result.Info.Policy) == "" || strings.TrimSpace(result.Info.Version) == "" || strings.TrimSpace(result.Info.ConfigVersion) == "" {
		return errors.New("relevance gate info requires policy, version, and config_version")
	}
	allowed := make(map[string]domain.RetrievedDocumentChunk, len(input))
	positions := make(map[string]int, len(input))
	for index, item := range input {
		allowed[item.Chunk.ID] = item
		positions[item.Chunk.ID] = index
	}
	if len(result.Decisions) != len(input) {
		return fmt.Errorf("returned %d decisions; expected one for each of %d candidates", len(result.Decisions), len(input))
	}
	acceptedDecisions := make(map[string]struct{}, len(result.Items))
	for index, decision := range result.Decisions {
		candidate := input[index]
		if decision.DocumentID != candidate.Document.ID || decision.ChunkID != candidate.Chunk.ID {
			return fmt.Errorf("decision %d does not match candidate %q", index, candidate.Chunk.ID)
		}
		if decision.Confidence != "high" && decision.Confidence != "medium" && decision.Confidence != "low" {
			return fmt.Errorf("decision %d has invalid confidence %q", index, decision.Confidence)
		}
		if strings.TrimSpace(decision.FilterReason) == "" {
			return fmt.Errorf("decision %d is missing filter_reason", index)
		}
		if decision.Accepted != (decision.Confidence != "low") {
			return fmt.Errorf("decision %d has inconsistent accepted state", index)
		}
		if decision.Accepted {
			acceptedDecisions[decision.ChunkID] = struct{}{}
		}
	}
	seen := make(map[string]struct{}, len(result.Items))
	previousPosition := -1
	for index, item := range result.Items {
		candidate, ok := allowed[item.Chunk.ID]
		if !ok || strings.TrimSpace(item.Chunk.ID) == "" {
			return fmt.Errorf("item %d references unknown candidate %q", index, item.Chunk.ID)
		}
		if _, duplicate := seen[item.Chunk.ID]; duplicate {
			return fmt.Errorf("item %d duplicates chunk %q", index, item.Chunk.ID)
		}
		seen[item.Chunk.ID] = struct{}{}
		position := positions[item.Chunk.ID]
		if position <= previousPosition {
			return fmt.Errorf("item %d changes reranker ordering", index)
		}
		previousPosition = position
		if !reflect.DeepEqual(withoutRelevanceGateOutput(candidate), withoutRelevanceGateOutput(item)) {
			return fmt.Errorf("item %d modifies ranked candidate fields", index)
		}
		if item.RerankRank != index+1 {
			return fmt.Errorf("item %d has rerank_rank %d; expected %d", index, item.RerankRank, index+1)
		}
		if item.Confidence != "high" && item.Confidence != "medium" {
			return fmt.Errorf("item %d has invalid confidence %q", index, item.Confidence)
		}
		if strings.TrimSpace(item.FilterReason) == "" {
			return fmt.Errorf("item %d is missing filter_reason", index)
		}
		if _, ok := acceptedDecisions[item.Chunk.ID]; !ok {
			return fmt.Errorf("item %d is not accepted by its relevance decision", index)
		}
		delete(acceptedDecisions, item.Chunk.ID)
	}
	if len(acceptedDecisions) != 0 {
		return errors.New("relevance decisions accept candidates missing from output")
	}
	return nil
}

func withoutRelevanceGateOutput(item domain.RetrievedDocumentChunk) domain.RetrievedDocumentChunk {
	item.RerankRank = 0
	item.MatchedTerms = nil
	item.EvidenceScore = 0
	item.EvidenceCoverage = 0
	item.Confidence = ""
	item.FilterReason = ""
	return item
}
