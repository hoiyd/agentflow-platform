package rag

import (
	"sort"

	"agentflow-platform/apps/api/internal/domain"
)

func NormalizeSearchLimit(limit int) int {
	if limit <= 0 {
		return 5
	}
	if limit > 10 {
		return 10
	}
	return limit
}

func CandidateLimit(limit int) int {
	if limit <= 0 {
		limit = 5
	}
	candidateLimit := limit * 4
	if candidateLimit < 10 {
		candidateLimit = 10
	}
	if candidateLimit > 20 {
		candidateLimit = 20
	}
	return candidateLimit
}

func Rerank(query string, items []domain.RetrievedDocumentChunk, limit int) []domain.RetrievedDocumentChunk {
	if len(items) == 0 {
		return items
	}
	queryTerms := QueryTerms(query)
	for index := range items {
		items[index].LexicalBoost = lexicalBoost(query, queryTerms, items[index].Chunk.Content)
		items[index].MetadataBoost = metadataBoost(query, queryTerms, items[index])
		items[index].RerankScore = normalizedRRFScore(items[index].RRFScore) + items[index].LexicalBoost + items[index].MetadataBoost
		items[index].MatchedTerms = matchedTerms(query, queryTerms, items[index])
		items[index].EvidenceCoverage = evidenceCoverage(queryTerms, items[index].MatchedTerms)
		items[index].EvidenceScore = evidenceScore(query, queryTerms, items[index])
		items[index].Confidence, items[index].FilterReason = relevanceConfidence(items[index])
		items[index].RerankScore += items[index].EvidenceScore
	}
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].RerankScore > items[j].RerankScore
	})

	selected := make([]domain.RetrievedDocumentChunk, 0, minInt(limit, len(items)))
	usedDocuments := map[string]int{}
	for _, item := range items {
		if len(selected) >= limit {
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
	if len(selected) < limit {
		seenChunks := map[string]bool{}
		for _, item := range selected {
			seenChunks[item.Chunk.ID] = true
		}
		for _, item := range items {
			if len(selected) >= limit {
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
	return selected
}

func ApplyRelevanceGate(items []domain.RetrievedDocumentChunk) []domain.RetrievedDocumentChunk {
	if len(items) == 0 {
		return items
	}
	filtered := make([]domain.RetrievedDocumentChunk, 0, len(items))
	for _, item := range items {
		if item.Confidence == "low" {
			continue
		}
		filtered = append(filtered, item)
	}
	for index := range filtered {
		filtered[index].RerankRank = index + 1
	}
	return filtered
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
