package rag

import (
	"reflect"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
)

func TestAssignCitationSourcesUsesFinalContextOrderAndSourceChunks(t *testing.T) {
	items := []domain.RetrievedDocumentChunk{
		{
			Document: domain.Document{ID: "doc-a", Title: "Deploy guide"},
			Chunk: domain.DocumentChunk{ID: "merged-a", ChunkSource: domain.ChunkSource{
				DocumentVersion: "v2", SectionPath: []string{"Deploy", "Checks"}, StartOffset: 10, EndOffset: 80,
			}},
			SourceChunkIDs: []string{"chunk-a1", "chunk-a2"},
		},
		{Document: domain.Document{ID: "doc-b", Title: "Runbook"}, Chunk: domain.DocumentChunk{ID: "chunk-b"}},
	}

	assigned, sources := AssignCitationSources(items)
	if assigned[0].SourceID != "S1" || assigned[1].SourceID != "S2" {
		t.Fatalf("unexpected source IDs: %#v", assigned)
	}
	if items[0].SourceID != "" {
		t.Fatal("assignment mutated caller-owned context items")
	}
	if sources[0].ChunkID != "merged-a" || !reflect.DeepEqual(sources[0].SourceChunkIDs, []string{"chunk-a1", "chunk-a2"}) {
		t.Fatalf("merged source metadata was lost: %#v", sources[0])
	}
	if !reflect.DeepEqual(sources[1].SourceChunkIDs, []string{"chunk-b"}) {
		t.Fatalf("single chunk source was not normalized: %#v", sources[1])
	}
}

func TestResolveCitationsAcceptsOnlyTrustedSources(t *testing.T) {
	sources := []domain.RAGCitation{
		{SourceID: "S1", DocumentID: "doc-a", ChunkID: "chunk-a"},
		{SourceID: "S2", DocumentID: "doc-b", ChunkID: "chunk-b"},
	}

	resolved, invalid := ResolveCitations("First [S2], repeated [s2], trusted [S1], invented [S9], malformed [SX], zero [S0].", sources)
	if !reflect.DeepEqual(citationIDs(resolved), []string{"S2", "S1"}) {
		t.Fatalf("unexpected resolved citations: %#v", resolved)
	}
	if !reflect.DeepEqual(invalid, []string{"S9", "S0"}) {
		t.Fatalf("unexpected invalid source IDs: %#v", invalid)
	}
}

func citationIDs(citations []domain.RAGCitation) []string {
	ids := make([]string, 0, len(citations))
	for _, citation := range citations {
		ids = append(ids, citation.SourceID)
	}
	return ids
}
