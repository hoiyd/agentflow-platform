package rag

import (
	"math"
	"slices"

	"agentflow-platform/apps/api/internal/domain"
)

func finiteScore(score float64) bool { return !math.IsNaN(score) && !math.IsInf(score, 0) }

// Stages receive detached inputs, never the Pipeline's candidate authority.
// Metadata from persistence has JSON shapes; strings and numbers are immutable.
func stageCandidates(input []domain.RetrievedDocumentChunk) []domain.RetrievedDocumentChunk {
	items := slices.Clone(input)
	for index := range items {
		item := &items[index]
		item.Document.Metadata = cloneStageMetadata(item.Document.Metadata)
		item.Chunk.Metadata = cloneStageMetadata(item.Chunk.Metadata)
		item.Chunk.Document.Metadata = cloneStageMetadata(item.Chunk.Document.Metadata)
		item.Chunk.SectionPath = slices.Clone(item.Chunk.SectionPath)
		item.MatchedTerms = slices.Clone(item.MatchedTerms)
		item.SourceChunkIDs = slices.Clone(item.SourceChunkIDs)
		item.MatchedChunkIDs = slices.Clone(item.MatchedChunkIDs)
	}
	return items
}

func cloneStageMetadata(input map[string]any) map[string]any {
	if input == nil {
		return nil
	}
	output := make(map[string]any, len(input))
	for key, value := range input {
		output[key] = cloneStageValue(value)
	}
	return output
}

func cloneStageValue(value any) any {
	switch value := value.(type) {
	case map[string]any:
		return cloneStageMetadata(value)
	case []any:
		items := make([]any, len(value))
		for index, item := range value {
			items[index] = cloneStageValue(item)
		}
		return items
	case []string:
		return slices.Clone(value)
	case []byte:
		return slices.Clone(value)
	default:
		return value
	}
}
