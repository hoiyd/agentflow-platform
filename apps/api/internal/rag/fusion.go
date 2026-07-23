package rag

import (
	"sort"

	"agentflow-platform/apps/api/internal/domain"
)

const rrfRankConstant = 60

// ReciprocalRankFusion combines independent recall rankings without comparing
// provider-specific dense and lexical scores.
func ReciprocalRankFusion(items []domain.RetrievedDocumentChunk) []domain.RetrievedDocumentChunk {
	for index := range items {
		items[index].RRFScore = reciprocalRank(items[index].VectorRank) + reciprocalRank(items[index].LexicalRank)
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].RRFScore == items[j].RRFScore {
			return items[i].Chunk.ID < items[j].Chunk.ID
		}
		return items[i].RRFScore > items[j].RRFScore
	})
	for index := range items {
		items[index].FusionRank = index + 1
	}
	return items
}

func reciprocalRank(rank int) float64 {
	if rank <= 0 {
		return 0
	}
	return 1 / float64(rrfRankConstant+rank)
}

func normalizedRRFScore(score float64) float64 {
	if score <= 0 {
		return 0
	}
	maximum := 2.0 / float64(rrfRankConstant+1)
	if score >= maximum {
		return 1
	}
	return score / maximum
}
