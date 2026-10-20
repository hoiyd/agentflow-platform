package rag

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
)

// Accepted recall alone cannot hide forbidden parent Context. Merged Context
// needs its own content hash because the transformation clears Chunk.ContentHash.
func TestEvaluationContextEvidenceAndLeakGate(t *testing.T) {
	allowed := domain.RetrievedDocumentChunk{Document: domain.Document{ID: "atlas", WorkspaceID: "1", SourceURI: "atlas.md", Metadata: map[string]any{"product": "Atlas"}},
		Chunk: domain.DocumentChunk{ID: "merged", Content: "merged release instructions", TokenCount: 5}, SourceChunkIDs: []string{"one", "two"}}
	c := domain.RAGEvaluationCase{Query: "release", ExpectedSources: []domain.RAGGoldenSource{{SourceURI: "atlas.md"}}}
	response := domain.DocumentSearchResponse{Items: []domain.RetrievedDocumentChunk{allowed}, ContextItems: []domain.RetrievedDocumentChunk{allowed}}
	search := domain.DocumentSearch{WorkspaceID: "1", Metadata: map[string]string{"product": "Atlas"}}
	result := EvaluateSearchResult(c, search, response, time.Now())
	if !result.Hit || result.Evidence.ContextSources[0].ChunkHash == "" {
		t.Fatalf("merged Context lacks hash: %+v", result.Evidence)
	}
	data, _ := json.Marshal(result.Evidence)
	if !strings.Contains(string(data), `"source_chunk_ids":["one","two"]`) {
		t.Fatalf("lost constituent identities: %s", data)
	}
	foreign := allowed
	foreign.Document = domain.Document{ID: "foreign", WorkspaceID: "2", SourceURI: "private.md"}
	response.ContextItems = append(response.ContextItems, foreign)
	result = EvaluateSearchResult(c, search, response, time.Now())
	if result.Hit || result.Evidence.ContextLeakCount != 1 || !strings.Contains(result.FailureReason, "out-of-scope") {
		t.Fatalf("Context leak passed: %+v", result)
	}
	// A forbidden original Chunk must remain recognizable after merging.
	original := allowed
	original.Chunk.ID, original.SourceChunkIDs = "one", []string{"one"}
	response.Items, response.ContextItems = []domain.RetrievedDocumentChunk{original}, []domain.RetrievedDocumentChunk{allowed}
	c.ForbiddenSources = []domain.RAGGoldenSource{{ChunkID: "two"}}
	result = EvaluateSearchResult(c, search, response, time.Now())
	if result.Hit || result.Evidence.ContextLeakCount != 1 {
		t.Fatalf("merged forbidden Chunk escaped: %+v", result)
	}
	c.ForbiddenSources = nil
	c.ExpectedSources = []domain.RAGGoldenSource{{ChunkID: "one"}, {ChunkID: "two"}}
	result = EvaluateSearchResult(c, search, response, time.Now())
	if result.Evidence.ContextMatchedSources != 2 {
		t.Fatalf("merged source denominator lost: %+v", result.Evidence)
	}
}
