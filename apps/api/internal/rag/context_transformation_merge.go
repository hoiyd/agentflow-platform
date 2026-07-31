package rag

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"agentflow-platform/apps/api/internal/domain"
)

func mergeAdjacentContextChunks(items []domain.RetrievedDocumentChunk) ([]domain.RetrievedDocumentChunk, int) {
	if len(items) == 0 {
		return []domain.RetrievedDocumentChunk{}, 0
	}
	merged := make([]domain.RetrievedDocumentChunk, 0, len(items))
	current := items[0]
	lastSource := items[0]
	merges := 0
	for _, next := range items[1:] {
		if next.Chunk.ChunkIndex != lastSource.Chunk.ChunkIndex+1 {
			merged = append(merged, current)
			current = next
			lastSource = next
			continue
		}
		current = mergeContextChunkPair(current, lastSource, next)
		lastSource = next
		merges++
	}
	merged = append(merged, current)
	return merged, merges
}

func mergeContextChunkPair(current domain.RetrievedDocumentChunk, lastSource domain.RetrievedDocumentChunk, next domain.RetrievedDocumentChunk) domain.RetrievedDocumentChunk {
	leftSourceIDs := contextSourceChunkIDs(current)
	rightSourceIDs := contextSourceChunkIDs(next)
	sourceIDs := appendUniqueStrings(leftSourceIDs, rightSourceIDs...)
	matchedIDs := appendUniqueStrings(contextMatchedChunkIDs(current), contextMatchedChunkIDs(next)...)

	if shouldUseContextRanking(next, current) {
		copyContextRanking(&current, next)
	}
	current.Chunk.ID = mergedContextChunkID(sourceIDs)
	current.Chunk.Content = mergeContextContent(current.Chunk.Content, lastSource.Chunk, next.Chunk)
	current.Chunk.TokenCount += contextChunkTokens(next.Chunk)
	current.Chunk.StartOffset = min(current.Chunk.StartOffset, next.Chunk.StartOffset)
	current.Chunk.EndOffset = max(current.Chunk.EndOffset, next.Chunk.EndOffset)
	current.Chunk.ContentHash = ""
	current.Chunk.ParentID = sharedParentID(current.Chunk.ParentID, next.Chunk.ParentID)
	current.Chunk.SectionPath = commonSectionPath(current.Chunk.SectionPath, next.Chunk.SectionPath)
	current.SourceChunkIDs = sourceIDs
	current.MatchedChunkIDs = matchedIDs
	current.MergedChunkCount = len(sourceIDs)
	if len(matchedIDs) > 0 {
		current.MatchedChunkID = matchedIDs[0]
	}
	current.Chunk.Document = current.Document
	return current
}

func mergeContextContent(current string, lastSource domain.DocumentChunk, next domain.DocumentChunk) string {
	current = strings.TrimSpace(current)
	nextContent := strings.TrimSpace(next.Content)
	overlapped := false
	if sameSectionPath(lastSource.SectionPath, next.SectionPath) {
		heading := markdownHeadingContext(next.SectionPath)
		nextContent = strings.TrimSpace(strings.TrimPrefix(nextContent, heading+"\n\n"))
	}
	if overlap := lastSource.EndOffset - next.StartOffset; overlap > 0 {
		nextContent = trimContextBytePrefix(nextContent, overlap)
		overlapped = true
	}
	if nextContent == "" || strings.Contains(current, nextContent) {
		return current
	}
	if current == "" {
		return nextContent
	}
	if overlapped {
		return current + nextContent
	}
	return current + "\n\n" + nextContent
}

func trimContextBytePrefix(content string, count int) string {
	if count <= 0 {
		return content
	}
	bytes := []byte(content)
	if count >= len(bytes) {
		return ""
	}
	for count < len(bytes) && bytes[count]&0xc0 == 0x80 {
		count++
	}
	return strings.TrimSpace(string(bytes[count:]))
}

func mergedContextChunkID(sourceIDs []string) string {
	digest := sha256.Sum256([]byte(strings.Join(sourceIDs, "\x00")))
	return "context_merged_" + hex.EncodeToString(digest[:8])
}

func sharedParentID(left string, right string) string {
	if left == right {
		return left
	}
	return ""
}

func commonSectionPath(left []string, right []string) []string {
	limit := min(len(left), len(right))
	common := make([]string, 0, limit)
	for index := 0; index < limit; index++ {
		if left[index] != right[index] {
			break
		}
		common = append(common, left[index])
	}
	return common
}

func sameSectionPath(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
