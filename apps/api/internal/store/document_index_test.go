package store

import (
	"strings"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/failure"
)

func TestPrepareDocumentWriteRejectsInvalidEmbeddingIdentity(t *testing.T) {
	document := domain.Document{Title: "Policy", Content: "current policy"}
	chunk := domain.DocumentChunk{Content: "current policy"}
	tests := []struct {
		name       string
		document   domain.Document
		chunks     []domain.DocumentChunk
		embeddings []domain.DocumentChunkEmbedding
	}{
		{name: "length mismatch", document: document, chunks: []domain.DocumentChunk{chunk}},
		{name: "dimension mismatch", document: document, chunks: []domain.DocumentChunk{chunk}, embeddings: []domain.DocumentChunkEmbedding{{Provider: "test", Model: "v1", Dimensions: 3, Embedding: []float64{1, 0}}}},
		{name: "mixed models", document: document, chunks: []domain.DocumentChunk{chunk, chunk}, embeddings: []domain.DocumentChunkEmbedding{{Provider: "test", Model: "v1", Embedding: []float64{1}}, {Provider: "test", Model: "v2", Embedding: []float64{1}}}},
		{name: "partial vectors", document: document, chunks: []domain.DocumentChunk{chunk, chunk}, embeddings: []domain.DocumentChunkEmbedding{{Provider: "test", Model: "v1", Embedding: []float64{1}}, {Provider: "test", Model: "v1"}}},
		{name: "declared identity mismatch", document: domain.Document{Title: "Policy", Content: "current policy", IndexIdentity: domain.DocumentIndexIdentity{ChunkerVersion: domain.DocumentChunkerVersion, EmbeddingProvider: "test", EmbeddingModel: "other", EmbeddingDimensions: 1}}, chunks: []domain.DocumentChunk{chunk}, embeddings: []domain.DocumentChunkEmbedding{{Provider: "test", Model: "v1", Embedding: []float64{1}}}},
		{name: "identity without vectors", document: domain.Document{Title: "Policy", Content: "current policy", IndexIdentity: domain.DocumentIndexIdentity{ChunkerVersion: domain.DocumentChunkerVersion, EmbeddingProvider: "test", EmbeddingModel: "v1", EmbeddingDimensions: 1}}, chunks: []domain.DocumentChunk{chunk}, embeddings: []domain.DocumentChunkEmbedding{{Provider: "test", Model: "v1"}}},
		{name: "source key too long", document: domain.Document{SourceKey: strings.Repeat("a", 513), Title: "Policy", Content: "current policy"}},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			if _, _, _, err := PrepareDocumentWrite(testCase.document, testCase.chunks, testCase.embeddings); err == nil {
				t.Fatal("invalid index identity was accepted")
			}
		})
	}
}

func TestPrepareDocumentWriteNormalizesTimestampPrecision(t *testing.T) {
	inputTime := time.Date(2026, 9, 8, 1, 2, 3, 456789123, time.FixedZone("fixture", 8*60*60))
	document, chunks, embeddings, err := PrepareDocumentWrite(
		domain.Document{Title: "Policy", Content: "current policy", CreatedAt: inputTime},
		[]domain.DocumentChunk{{Content: "current policy", CreatedAt: inputTime}},
		[]domain.DocumentChunkEmbedding{{CreatedAt: inputTime}},
	)
	if err != nil {
		t.Fatal(err)
	}
	want := inputTime.UTC().Truncate(time.Microsecond)
	if !document.CreatedAt.Equal(want) || !chunks[0].CreatedAt.Equal(want) || !embeddings[0].CreatedAt.Equal(want) ||
		document.UpdatedAt.Nanosecond()%1000 != 0 {
		t.Fatalf("timestamps were not normalized: document=%s updated=%s chunk=%s embedding=%s", document.CreatedAt, document.UpdatedAt, chunks[0].CreatedAt, embeddings[0].CreatedAt)
	}
}

func TestNormalizeFileDocumentIndexesBackfillsAndRemovesStaleSources(t *testing.T) {
	oldTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	newTime := oldTime.Add(time.Hour)
	data := emptyFileData()
	data.Documents = []domain.Document{
		{ID: "old", WorkspaceID: "workspace-a", SourceURI: "refund-policy.md", Version: "2.4", UpdatedAt: oldTime},
		{ID: "current", WorkspaceID: "workspace-a", SourceURI: "refund-policy.md", Version: "3.2", UpdatedAt: newTime},
	}
	data.DocumentContents = map[string]string{"old": "30 days", "current": "14 days"}
	data.DocumentChunks = []domain.DocumentChunk{{ID: "old-chunk", DocumentID: "old"}, {ID: "current-chunk", DocumentID: "current"}}
	data.ChunkEmbeddings = []domain.DocumentChunkEmbedding{
		{ChunkID: "old-chunk", Provider: "test", Model: "embedding-v1", Dimensions: 2, Embedding: []float64{1, 0}},
		{ChunkID: "current-chunk", Provider: "test", Model: "embedding-v1", Dimensions: 2, Embedding: []float64{1, 0}},
	}

	if !normalizeFileDocumentIndexes(&data) {
		t.Fatal("legacy index was not migrated")
	}
	if len(data.Documents) != 1 || data.Documents[0].ID != "current" || data.Documents[0].SourceKey != "refund-policy.md" {
		t.Fatalf("stale source survived migration: %#v", data.Documents)
	}
	if data.Documents[0].IndexIdentity != (domain.DocumentIndexIdentity{ChunkerVersion: domain.DocumentChunkerVersion, EmbeddingProvider: "test", EmbeddingModel: "embedding-v1", EmbeddingDimensions: 2}) {
		t.Fatalf("index identity was not backfilled: %#v", data.Documents[0].IndexIdentity)
	}
	if len(data.DocumentChunks) != 1 || data.DocumentChunks[0].ID != "current-chunk" || len(data.ChunkEmbeddings) != 1 || data.ChunkEmbeddings[0].ChunkID != "current-chunk" || data.DocumentContents["old"] != "" {
		t.Fatalf("stale child records survived migration: chunks=%#v embeddings=%#v contents=%#v", data.DocumentChunks, data.ChunkEmbeddings, data.DocumentContents)
	}
	if normalizeFileDocumentIndexes(&data) {
		t.Fatal("normalized index should not migrate twice")
	}
}

func TestDocumentVersionConflictHasStableFailureCode(t *testing.T) {
	err := DocumentVersionConflict(domain.Document{ContentHash: "old"}, domain.Document{SourceKey: "policy", Version: "1", ContentHash: "new"})
	if !IsDocumentVersionConflict(err) || failure.Describe(err).Code != "document_version_conflict" {
		t.Fatalf("unexpected conflict contract: %v", err)
	}
}
