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
	heuristicRelevanceGateVersion     = "heuristic-relevance-gate-v3"
	defaultRelevanceGateConfigVersion = "heuristic-relevance-hardened-v2"
	defaultMinimumEvidenceCoverage    = 0.25
)

type RelevanceGateRequest struct {
	Query      string
	Candidates []domain.RetrievedDocumentChunk
	Reranker   domain.RerankerInfo
}

// RelevanceDecision cannot rewrite recall or ranking evidence. The Pipeline
// adds those signals to the public audit record from its retained candidate.
type RelevanceDecision struct {
	DocumentID       string
	ChunkID          string
	MatchedTerms     []string
	EvidenceScore    float64
	EvidenceCoverage float64
	IdentifierMatch  string
	Confidence       string
	FilterReason     string
	Accepted         bool
}

type RelevanceGateResult struct {
	Decisions []RelevanceDecision
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
	decisions := make([]RelevanceDecision, 0, len(request.Candidates))
	queryTerms := QueryTerms(request.Query)
	for _, item := range request.Candidates {
		item.MatchedTerms = matchedTerms(request.Query, queryTerms, item)
		item.EvidenceCoverage = evidenceCoverage(queryTerms, item.MatchedTerms)
		item.EvidenceScore = evidenceScore(request.Query, queryTerms, item)
		identifier := matchedIdentifier(request.Query, item)
		item.Confidence, item.FilterReason = relevanceConfidence(item, identifier != "", request.Reranker, g.config)
		decisions = append(decisions, RelevanceDecision{
			DocumentID: item.Document.ID, ChunkID: item.Chunk.ID,
			MatchedTerms:  append([]string{}, item.MatchedTerms...),
			EvidenceScore: item.EvidenceScore, EvidenceCoverage: item.EvidenceCoverage,
			IdentifierMatch: identifier, Confidence: item.Confidence,
			FilterReason: item.FilterReason, Accepted: item.Confidence != "low",
		})
	}
	return RelevanceGateResult{Decisions: decisions, Info: g.Info()}, nil
}

type gateSelection struct {
	items     []domain.RetrievedDocumentChunk
	decisions []domain.RelevanceGateDecision
}

func applyRelevanceGateResult(input []domain.RetrievedDocumentChunk, result RelevanceGateResult) (gateSelection, error) {
	if strings.TrimSpace(result.Info.Policy) == "" || strings.TrimSpace(result.Info.Version) == "" || strings.TrimSpace(result.Info.ConfigVersion) == "" {
		return gateSelection{}, errors.New("relevance gate info requires policy, version, and config_version")
	}
	if len(result.Decisions) != len(input) {
		return gateSelection{}, fmt.Errorf("returned %d decisions; expected one for each of %d candidates", len(result.Decisions), len(input))
	}
	selection := gateSelection{items: make([]domain.RetrievedDocumentChunk, 0, len(input)), decisions: make([]domain.RelevanceGateDecision, 0, len(input))}
	for index, decision := range result.Decisions {
		candidate := input[index]
		// Ranked input is already unique. Exact positional identity rejects
		// unknown, duplicated and reordered decisions without a second lookup.
		if strings.TrimSpace(decision.ChunkID) == "" || strings.TrimSpace(decision.DocumentID) == "" || decision.DocumentID != candidate.Document.ID || decision.ChunkID != candidate.Chunk.ID {
			return gateSelection{}, fmt.Errorf("decision %d does not match candidate %q or ordering", index, candidate.Chunk.ID)
		}
		if decision.Confidence != "high" && decision.Confidence != "medium" && decision.Confidence != "low" {
			return gateSelection{}, fmt.Errorf("decision %d has invalid confidence %q", index, decision.Confidence)
		}
		if strings.TrimSpace(decision.FilterReason) == "" {
			return gateSelection{}, fmt.Errorf("decision %d is missing filter_reason", index)
		}
		if decision.Accepted != (decision.Confidence != "low") {
			return gateSelection{}, fmt.Errorf("decision %d has inconsistent accepted state", index)
		}
		if !finiteScore(decision.EvidenceScore) || decision.EvidenceScore < 0 || !finiteScore(decision.EvidenceCoverage) || decision.EvidenceCoverage < 0 || decision.EvidenceCoverage > 1 {
			return gateSelection{}, fmt.Errorf("decision %d has invalid evidence", index)
		}
		candidate.MatchedTerms = append([]string{}, decision.MatchedTerms...)
		candidate.EvidenceScore = decision.EvidenceScore
		candidate.EvidenceCoverage = decision.EvidenceCoverage
		candidate.Confidence = decision.Confidence
		candidate.FilterReason = decision.FilterReason
		selection.decisions = append(selection.decisions, domain.RelevanceGateDecision{
			DocumentID: candidate.Document.ID, ChunkID: candidate.Chunk.ID,
			LexicalRank: candidate.LexicalRank, LexicalScore: candidate.LexicalScore,
			Similarity: candidate.Similarity, RerankScore: candidate.RerankScore,
			MatchedTerms: append([]string{}, candidate.MatchedTerms...), EvidenceScore: candidate.EvidenceScore,
			EvidenceCoverage: candidate.EvidenceCoverage, IdentifierMatch: decision.IdentifierMatch,
			Confidence: candidate.Confidence, FilterReason: candidate.FilterReason, Accepted: decision.Accepted,
		})
		if decision.Accepted {
			candidate.RerankRank = len(selection.items) + 1
			selection.items = append(selection.items, candidate)
		}
	}
	return selection, nil
}
