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

	selected := selectWithDocumentDiversity(items, request.Limit)
	sort.SliceStable(selected, func(i, j int) bool {
		return selected[i].RerankScore > selected[j].RerankScore
	})
	for index := range selected {
		selected[index].RerankRank = index + 1
	}
	return RerankResult{Items: selected, Info: r.Info()}, nil
}

// selectWithDocumentDiversity recomputes effective scores after every pick so
// a diversity penalty can change Top-K membership, not only final ordering.
func selectWithDocumentDiversity(items []domain.RetrievedDocumentChunk, limit int) []domain.RetrievedDocumentChunk {
	selected := make([]domain.RetrievedDocumentChunk, 0, minInt(limit, len(items)))
	remaining := append([]domain.RetrievedDocumentChunk(nil), items...)
	usedDocuments := map[string]int{}
	multipleDocuments := hasMultipleDocuments(items)

	for len(selected) < limit && len(remaining) > 0 {
		unusedDocumentAvailable := hasUnusedDocument(remaining, usedDocuments)
		bestIndex := -1
		var best domain.RetrievedDocumentChunk
		for index, candidate := range remaining {
			documentUses := usedDocuments[candidate.Document.ID]
			if documentUses >= 2 && unusedDocumentAvailable {
				continue
			}
			if multipleDocuments && documentUses > 0 {
				candidate.DiversityPenalty = 0.04 * float64(documentUses)
				candidate.RerankScore -= candidate.DiversityPenalty
			}
			if bestIndex == -1 || candidate.RerankScore > best.RerankScore {
				bestIndex = index
				best = candidate
			}
		}

		selected = append(selected, best)
		usedDocuments[best.Document.ID]++
		remaining = append(remaining[:bestIndex], remaining[bestIndex+1:]...)
	}
	return selected
}

func hasUnusedDocument(items []domain.RetrievedDocumentChunk, usedDocuments map[string]int) bool {
	for _, item := range items {
		if usedDocuments[item.Document.ID] == 0 {
			return true
		}
	}
	return false
}

func hasMultipleDocuments(items []domain.RetrievedDocumentChunk) bool {
	if len(items) < 2 {
		return false
	}
	firstDocumentID := items[0].Document.ID
	for _, item := range items[1:] {
		if item.Document.ID != firstDocumentID {
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
