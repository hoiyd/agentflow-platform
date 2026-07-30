package rag

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strconv"
	"strings"

	"agentflow-platform/apps/api/internal/domain"
)

// ContextTransformationVersion identifies the current deduplication and merge
// policy while the surrounding transformer remains open to additional shaping steps.
const ContextTransformationVersion = "context-dedup-merge-v1"

type contextDocumentGroup struct {
	items []domain.RetrievedDocumentChunk
}

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

func contextRolePriority(role string) int {
	switch role {
	case domain.ContextRoleMatchedChild:
		return 3
	case domain.ContextRoleParent:
		return 2
	case domain.ContextRoleAdjacent:
		return 1
	default:
		return 0
	}
}

func shouldUseContextRanking(candidate domain.RetrievedDocumentChunk, current domain.RetrievedDocumentChunk) bool {
	candidatePriority := contextRolePriority(candidate.ContextRole)
	currentPriority := contextRolePriority(current.ContextRole)
	if candidatePriority != currentPriority {
		return candidatePriority > currentPriority
	}
	if candidate.RerankRank > 0 && (current.RerankRank == 0 || candidate.RerankRank < current.RerankRank) {
		return true
	}
	return candidate.RerankScore > current.RerankScore
}

func copyContextRanking(target *domain.RetrievedDocumentChunk, source domain.RetrievedDocumentChunk) {
	target.Similarity = source.Similarity
	target.RecencyBoost = source.RecencyBoost
	target.Score = source.Score
	target.VectorRank = source.VectorRank
	target.LexicalRank = source.LexicalRank
	target.LexicalScore = source.LexicalScore
	target.RRFScore = source.RRFScore
	target.FusionRank = source.FusionRank
	target.RerankRank = source.RerankRank
	target.LexicalBoost = source.LexicalBoost
	target.MetadataBoost = source.MetadataBoost
	target.DiversityPenalty = source.DiversityPenalty
	target.RerankScore = source.RerankScore
	target.MatchedTerms = append([]string(nil), source.MatchedTerms...)
	target.EvidenceScore = source.EvidenceScore
	target.EvidenceCoverage = source.EvidenceCoverage
	target.Confidence = source.Confidence
	target.FilterReason = source.FilterReason
	target.ContextRole = source.ContextRole
}

func contextItemsTokens(items []domain.RetrievedDocumentChunk) int {
	total := 0
	for _, item := range items {
		total += contextChunkTokens(item.Chunk)
	}
	return total
}
