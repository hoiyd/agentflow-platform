package rag

import (
	"strings"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
)

func TestTransformContextDeduplicatesSources(t *testing.T) {
	first := contextTestItem("doc-1", "workspace-1", "chunk-1", "parent-1", 0, "same source", 3)
	first.Chunk.ContentHash = "source-hash"
	first.ContextRole = domain.ContextRoleParent
	first.MatchedChunkID = "chunk-1"
	duplicate := first
	duplicate.Chunk.ID = "chunk-copy"
	duplicate.ContextRole = domain.ContextRoleMatchedChild
	duplicate.MatchedChunkID = "chunk-copy"
	duplicate.RerankRank = 1

	items, info := TransformContext([]domain.RetrievedDocumentChunk{first, duplicate}, 10)

	if len(items) != 1 || items[0].Chunk.ID != "chunk-1" {
		t.Fatalf("expected first ranked source to survive deduplication, got %#v", items)
	}
	if items[0].ContextRole != domain.ContextRoleMatchedChild || strings.Join(items[0].SourceChunkIDs, ",") != "chunk-1,chunk-copy" || strings.Join(items[0].MatchedChunkIDs, ",") != "chunk-1,chunk-copy" {
		t.Fatalf("expected duplicate source traceability and highest-priority ranking, got %#v", items[0])
	}
	if info.InputChunks != 2 || info.OutputChunks != 1 || info.DuplicatesRemoved != 1 || info.AdjacentMerges != 0 || info.DocumentGroups != 1 {
		t.Fatalf("unexpected transformation info: %#v", info)
	}
}

func TestTransformContextGroupsDocumentsAndMergesAdjacentChunks(t *testing.T) {
	docA2 := transformedContextItem("doc-a", "a-2", 2, "# Operations\n\nthird", 5, 22, 27)
	docA2.ContextRole = domain.ContextRoleMatchedChild
	docA2.MatchedChunkID = "a-2"
	docA2.RerankRank = 1
	docA2.RerankScore = 0.9
	docB0 := transformedContextItem("doc-b", "b-0", 0, "separate document", 2, 0, 17)
	docB0.ContextRole = domain.ContextRoleMatchedChild
	docB0.MatchedChunkID = "b-0"
	docA0 := transformedContextItem("doc-a", "a-0", 0, "# Operations\n\nfirst", 3, 10, 15)
	docA0.ContextRole = domain.ContextRoleParent
	docA0.MatchedChunkID = "a-2"
	docA1 := transformedContextItem("doc-a", "a-1", 1, "# Operations\n\nsecond", 4, 16, 21)
	docA1.ContextRole = domain.ContextRoleParent
	docA1.MatchedChunkID = "a-2"

	items, info := TransformContext([]domain.RetrievedDocumentChunk{docA2, docB0, docA0, docA1}, 20)

	if len(items) != 2 || items[0].Document.ID != "doc-a" || items[1].Document.ID != "doc-b" {
		t.Fatalf("expected same-document grouping in first-seen order, got %#v", items)
	}
	merged := items[0]
	if merged.MergedChunkCount != 3 || strings.Join(merged.SourceChunkIDs, ",") != "a-0,a-1,a-2" {
		t.Fatalf("expected three source chunks in merge, got %#v", merged)
	}
	if strings.Count(merged.Chunk.Content, "# Operations") != 1 || !strings.Contains(merged.Chunk.Content, "first\n\nsecond\n\nthird") {
		t.Fatalf("expected ordered content without repeated heading context, got %q", merged.Chunk.Content)
	}
	if merged.ContextRole != domain.ContextRoleMatchedChild || merged.RerankRank != 1 || merged.Chunk.ContentHash != "" {
		t.Fatalf("expected matched-child ranking and synthetic source marker, got %#v", merged)
	}
	if info.InputChunks != 4 || info.OutputChunks != 2 || info.AdjacentMerges != 2 || info.DocumentGroups != 2 || info.Version != ContextTransformationVersion {
		t.Fatalf("unexpected transformation info: %#v", info)
	}
	if contextItemsTokens(items) != 14 {
		t.Fatalf("unexpected transformed tokens: %d", contextItemsTokens(items))
	}
}

func TestTransformContextRemovesFixedChunkOverlap(t *testing.T) {
	left := transformedContextItem("doc-1", "chunk-0", 0, "abcdef", 2, 0, 6)
	right := transformedContextItem("doc-1", "chunk-1", 1, "defghi", 2, 3, 9)

	items, info := TransformContext([]domain.RetrievedDocumentChunk{left, right}, 4)

	if len(items) != 1 || items[0].Chunk.Content != "abcdefghi" || info.AdjacentMerges != 1 {
		t.Fatalf("expected overlapping chunks to merge without repeated text, items=%#v info=%#v", items, info)
	}
}

func TestTransformContextDefensivelyHonorsTokenLimit(t *testing.T) {
	first := transformedContextItem("doc-a", "a-0", 0, "first", 5, 0, 5)
	second := transformedContextItem("doc-b", "b-0", 0, "second", 4, 0, 6)

	items, info := TransformContext([]domain.RetrievedDocumentChunk{first, second}, 5)

	if len(items) != 1 || items[0].Chunk.ID != "a-0" || contextItemsTokens(items) != 5 || info.DocumentGroups != 1 {
		t.Fatalf("token limit was not enforced: items=%#v info=%#v", items, info)
	}
}

func transformedContextItem(documentID string, chunkID string, index int, content string, tokens int, start int, end int) domain.RetrievedDocumentChunk {
	item := contextTestItem(documentID, "", chunkID, "parent-1", index, content, tokens)
	item.Chunk.SectionPath = []string{"Operations"}
	item.Chunk.StartOffset = start
	item.Chunk.EndOffset = end
	item.Chunk.DocumentVersion = "v1"
	item.Chunk.ContentHash = "hash-" + chunkID
	return item
}
