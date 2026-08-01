package rag

import (
	"context"
	"sort"
	"strings"

	"agentflow-platform/apps/api/internal/domain"
)

const (
	heuristicRerankerAlgorithm    = "heuristic"
	heuristicRerankerVersion      = "heuristic-reranker-v1"
	defaultHeuristicConfigVersion = "heuristic-default-v1"
)

// HeuristicRerankerConfig versions the complete scoring policy. Future
// revisions can add tunable fields without changing the Reranker interface.
type HeuristicRerankerConfig struct {
	ConfigVersion string
}

func DefaultHeuristicRerankerConfig() HeuristicRerankerConfig {
	return HeuristicRerankerConfig{ConfigVersion: defaultHeuristicConfigVersion}
}

type HeuristicReranker struct {
	config HeuristicRerankerConfig
}

var _ Reranker = (*HeuristicReranker)(nil)

func NewHeuristicReranker(config HeuristicRerankerConfig) *HeuristicReranker {
	config.ConfigVersion = strings.TrimSpace(config.ConfigVersion)
	if config.ConfigVersion == "" {
		config.ConfigVersion = defaultHeuristicConfigVersion
	}
	return &HeuristicReranker{config: config}
}

func (r *HeuristicReranker) Info() domain.RerankerInfo {
	configVersion := defaultHeuristicConfigVersion
	if r != nil && strings.TrimSpace(r.config.ConfigVersion) != "" {
		configVersion = r.config.ConfigVersion
	}
	return domain.RerankerInfo{
		Algorithm:     heuristicRerankerAlgorithm,
		Version:       heuristicRerankerVersion,
		ConfigVersion: configVersion,
	}
}

func (r *HeuristicReranker) Rerank(_ context.Context, request RerankRequest) (RerankResult, error) {
	items := request.Candidates
	if len(items) == 0 {
		return RerankResult{Items: items, Info: r.Info()}, nil
	}
	queryTerms := QueryTerms(request.Query)
	for index := range items {
		items[index].LexicalBoost = lexicalBoost(request.Query, queryTerms, items[index].Chunk.Content)
		items[index].MetadataBoost = metadataBoost(request.Query, queryTerms, items[index])
		items[index].RerankScore = normalizedRRFScore(items[index].RRFScore) + items[index].LexicalBoost + items[index].MetadataBoost
		items[index].MatchedTerms = matchedTerms(request.Query, queryTerms, items[index])
		items[index].EvidenceCoverage = evidenceCoverage(queryTerms, items[index].MatchedTerms)
		items[index].EvidenceScore = evidenceScore(request.Query, queryTerms, items[index])
		items[index].RerankScore += items[index].EvidenceScore
	}
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].RerankScore > items[j].RerankScore
	})

	selected := make([]domain.RetrievedDocumentChunk, 0, minInt(request.Limit, len(items)))
	usedDocuments := map[string]int{}
	for _, item := range items {
		if len(selected) >= request.Limit {
			break
		}
		documentUses := usedDocuments[item.Document.ID]
		if documentUses > 0 && hasUnselectedDocument(items, usedDocuments) {
			item.DiversityPenalty = 0.04 * float64(documentUses)
			item.RerankScore -= item.DiversityPenalty
			if documentUses >= 2 {
				continue
			}
		}
		selected = append(selected, item)
		usedDocuments[item.Document.ID]++
	}
	if len(selected) < request.Limit {
		seenChunks := map[string]bool{}
		for _, item := range selected {
			seenChunks[item.Chunk.ID] = true
		}
		for _, item := range items {
			if len(selected) >= request.Limit {
				break
			}
			if seenChunks[item.Chunk.ID] {
				continue
			}
			selected = append(selected, item)
		}
	}
	sort.SliceStable(selected, func(i, j int) bool {
		return selected[i].RerankScore > selected[j].RerankScore
	})
	for index := range selected {
		selected[index].RerankRank = index + 1
	}
	return RerankResult{Items: selected, Info: r.Info()}, nil
}

func hasUnselectedDocument(items []domain.RetrievedDocumentChunk, usedDocuments map[string]int) bool {
	for _, item := range items {
		if usedDocuments[item.Document.ID] == 0 {
			return true
		}
	}
	return false
}

func minInt(a int, b int) int {
	if a < b {
		return a
	}
	return b
}
