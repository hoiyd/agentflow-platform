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
	heuristicRelevanceGateVersion     = "heuristic-relevance-gate-v1"
	defaultRelevanceGateConfigVersion = "heuristic-relevance-default-v1"
)

type RelevanceGateRequest struct {
	Query      string
	Candidates []domain.RetrievedDocumentChunk
	Reranker   domain.RerankerInfo
}

type RelevanceGateResult struct {
	Items []domain.RetrievedDocumentChunk
	Info  domain.RelevanceGateInfo
}

// RelevanceGate owns confidence classification and filtering independently of
// candidate ranking. It must not trust Confidence supplied by a Reranker.
type RelevanceGate interface {
	Evaluate(context.Context, RelevanceGateRequest) (RelevanceGateResult, error)
}

type HeuristicRelevanceGateConfig struct {
	ConfigVersion string
}

func DefaultHeuristicRelevanceGateConfig() HeuristicRelevanceGateConfig {
	return HeuristicRelevanceGateConfig{ConfigVersion: defaultRelevanceGateConfigVersion}
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
	return &HeuristicRelevanceGate{config: config}
}

func (g *HeuristicRelevanceGate) Info() domain.RelevanceGateInfo {
	configVersion := defaultRelevanceGateConfigVersion
	if g != nil && strings.TrimSpace(g.config.ConfigVersion) != "" {
		configVersion = g.config.ConfigVersion
	}
	return domain.RelevanceGateInfo{
		Policy:        heuristicRelevanceGatePolicy,
		Version:       heuristicRelevanceGateVersion,
		ConfigVersion: configVersion,
	}
}

func (g *HeuristicRelevanceGate) Evaluate(_ context.Context, request RelevanceGateRequest) (RelevanceGateResult, error) {
	filtered := make([]domain.RetrievedDocumentChunk, 0, len(request.Candidates))
	queryTerms := QueryTerms(request.Query)
	for _, item := range request.Candidates {
		item.MatchedTerms = matchedTerms(request.Query, queryTerms, item)
		item.EvidenceCoverage = evidenceCoverage(queryTerms, item.MatchedTerms)
		item.EvidenceScore = evidenceScore(request.Query, queryTerms, item)
		item.Confidence, item.FilterReason = relevanceConfidence(item, request.Reranker)
		if item.Confidence == "low" {
			continue
		}
		item.RerankRank = len(filtered) + 1
		filtered = append(filtered, item)
	}
	return RelevanceGateResult{Items: filtered, Info: g.Info()}, nil
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
