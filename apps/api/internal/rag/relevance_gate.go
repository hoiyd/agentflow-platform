package rag

import (
	"context"
	"errors"
	"fmt"
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
	for _, item := range request.Candidates {
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
	allowed := make(map[string]struct{}, len(input))
	for _, item := range input {
		allowed[item.Chunk.ID] = struct{}{}
	}
	seen := make(map[string]struct{}, len(result.Items))
	for index, item := range result.Items {
		if _, ok := allowed[item.Chunk.ID]; !ok || strings.TrimSpace(item.Chunk.ID) == "" {
			return fmt.Errorf("item %d references unknown candidate %q", index, item.Chunk.ID)
		}
		if _, duplicate := seen[item.Chunk.ID]; duplicate {
			return fmt.Errorf("item %d duplicates chunk %q", index, item.Chunk.ID)
		}
		seen[item.Chunk.ID] = struct{}{}
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
