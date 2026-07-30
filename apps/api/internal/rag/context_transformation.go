package rag

import "agentflow-platform/apps/api/internal/domain"

// ContextTransformationVersion identifies the current deduplication and merge
// policy while the surrounding transformer remains open to additional shaping steps.
const ContextTransformationVersion = "context-dedup-merge-v1"

// TransformContext applies the versioned post-selection shaping pipeline.
// It currently deduplicates sources, groups documents, and merges adjacent chunks;
// the generic entry point allows later transformations without changing its role.
func TransformContext(items []domain.RetrievedDocumentChunk, maxTokens int) ([]domain.RetrievedDocumentChunk, domain.ContextTransformationInfo) {
	info := domain.ContextTransformationInfo{
		Version:     ContextTransformationVersion,
		InputChunks: len(items),
	}
	if len(items) == 0 || maxTokens <= 0 {
		return []domain.RetrievedDocumentChunk{}, info
	}

	unique := deduplicateContextSources(items)
	info.DuplicatesRemoved = len(items) - len(unique)
	groups := groupContextByDocument(unique)
	transformed := make([]domain.RetrievedDocumentChunk, 0, len(unique))
	usedTokens := 0
	for _, group := range groups {
		merged, merges := mergeAdjacentContextChunks(group.items)
		info.AdjacentMerges += merges
		groupIncluded := false
		for _, item := range merged {
			tokens := contextChunkTokens(item.Chunk)
			if usedTokens+tokens > maxTokens {
				continue
			}
			item.Chunk.TokenCount = tokens
			transformed = append(transformed, item)
			usedTokens += tokens
			groupIncluded = true
		}
		if groupIncluded {
			info.DocumentGroups++
		}
	}
	info.OutputChunks = len(transformed)
	return transformed, info
}

func contextItemsTokens(items []domain.RetrievedDocumentChunk) int {
	total := 0
	for _, item := range items {
		total += contextChunkTokens(item.Chunk)
	}
	return total
}
