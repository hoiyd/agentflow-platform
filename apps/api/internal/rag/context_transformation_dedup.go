package rag

import (
	"sort"
	"strconv"
	"strings"

	"agentflow-platform/apps/api/internal/domain"
)

type contextDocumentGroup struct {
	items []domain.RetrievedDocumentChunk
}

func deduplicateContextSources(items []domain.RetrievedDocumentChunk) []domain.RetrievedDocumentChunk {
	unique := make([]domain.RetrievedDocumentChunk, 0, len(items))
	indexes := make(map[string]int, len(items))
	for _, item := range items {
		key := contextSourceIdentity(item)
		index, exists := indexes[key]
		if exists {
			current := unique[index]
			if shouldUseContextRanking(item, current) {
				copyContextRanking(&current, item)
			}
			current.SourceChunkIDs = appendUniqueStrings(contextSourceChunkIDs(current), contextSourceChunkIDs(item)...)
			current.MatchedChunkIDs = appendUniqueStrings(contextMatchedChunkIDs(current), contextMatchedChunkIDs(item)...)
			current.MergedChunkCount = len(current.SourceChunkIDs)
			if len(current.MatchedChunkIDs) > 0 {
				current.MatchedChunkID = current.MatchedChunkIDs[0]
			}
			unique[index] = current
			continue
		}
		indexes[key] = len(unique)
		unique = append(unique, item)
	}
	return unique
}

func groupContextByDocument(items []domain.RetrievedDocumentChunk) []contextDocumentGroup {
	groups := make([]contextDocumentGroup, 0)
	indexes := make(map[string]int)
	for _, item := range items {
		key := contextDocumentIdentity(item)
		index, exists := indexes[key]
		if !exists {
			index = len(groups)
			indexes[key] = index
			groups = append(groups, contextDocumentGroup{})
		}
		groups[index].items = append(groups[index].items, item)
	}
	for index := range groups {
		sort.SliceStable(groups[index].items, func(i, j int) bool {
			left := groups[index].items[i].Chunk
			right := groups[index].items[j].Chunk
			if left.ChunkIndex != right.ChunkIndex {
				return left.ChunkIndex < right.ChunkIndex
			}
			return left.ID < right.ID
		})
	}
	return groups
}

func contextSourceIdentity(item domain.RetrievedDocumentChunk) string {
	document := contextDocumentIdentity(item)
	if hash := strings.TrimSpace(item.Chunk.ContentHash); hash != "" {
		return document + "|hash:" + hash
	}
	if item.Chunk.EndOffset > item.Chunk.StartOffset {
		return document + "|range:" + item.Chunk.DocumentVersion + ":" + strconv.Itoa(item.Chunk.StartOffset) + ":" + strconv.Itoa(item.Chunk.EndOffset)
	}
	return document + "|chunk:" + strings.TrimSpace(item.Chunk.ID)
}

func contextDocumentIdentity(item domain.RetrievedDocumentChunk) string {
	documentID := strings.TrimSpace(item.Document.ID)
	if documentID == "" {
		documentID = strings.TrimSpace(item.Chunk.DocumentID)
	}
	return strings.TrimSpace(item.Document.WorkspaceID) + "|" + documentID
}

func contextSourceChunkIDs(item domain.RetrievedDocumentChunk) []string {
	if len(item.SourceChunkIDs) > 0 {
		return append([]string(nil), item.SourceChunkIDs...)
	}
	if id := strings.TrimSpace(item.Chunk.ID); id != "" {
		return []string{id}
	}
	return nil
}

func contextMatchedChunkIDs(item domain.RetrievedDocumentChunk) []string {
	ids := append([]string(nil), item.MatchedChunkIDs...)
	if id := strings.TrimSpace(item.MatchedChunkID); id != "" {
		ids = appendUniqueStrings(ids, id)
	}
	return ids
}

func appendUniqueStrings(items []string, values ...string) []string {
	seen := make(map[string]struct{}, len(items)+len(values))
	result := make([]string, 0, len(items)+len(values))
	for _, value := range append(append([]string(nil), items...), values...) {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
