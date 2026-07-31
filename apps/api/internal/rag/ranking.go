package rag

import "agentflow-platform/apps/api/internal/domain"

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
