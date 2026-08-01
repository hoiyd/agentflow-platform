package store

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
)

func TestFileStoreDocumentSearchUsesMetadataSimilarityAndRecency(t *testing.T) {
	store, err := NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	now := time.Now().UTC()
	document, err := store.CreateDocument(domain.Document{
		Title:      "Deployment Notes",
		SourceType: "text",
		Content:    "The deployment password is amber-9137.",
		Metadata:   map[string]any{"project": "agentflow"},
		CreatedAt:  now,
	}, []domain.DocumentChunk{
		{
			Content:    "The deployment password is amber-9137.",
			TokenCount: 10,
			Metadata:   map[string]any{"project": "agentflow"},
			CreatedAt:  now,
		},
	}, []domain.DocumentChunkEmbedding{
		{Embedding: []float64{1, 0}},
	})
	if err != nil {
		t.Fatalf("create document: %v", err)
	}
	if document.ChunkCount != 1 || document.EmbeddingCount != 1 {
		t.Fatalf("expected chunk and embedding counts, got %#v", document)
	}

	items, err := store.SearchDocumentChunks(domain.DocumentSearch{
		Query:     "deployment password",
		Embedding: []float64{1, 0},
		Metadata:  map[string]string{"project": "agentflow"},
		Limit:     3,
	})
	if err != nil {
		t.Fatalf("search document chunks: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected one retrieved chunk, got %d", len(items))
	}
	if !strings.Contains(items[0].Chunk.Content, "amber-9137") {
		t.Fatalf("expected deployment chunk, got %#v", items[0])
	}
	if items[0].Similarity <= 0 || items[0].RecencyBoost <= 0 || items[0].Score <= items[0].Similarity {
		t.Fatalf("expected similarity plus recency boost, got %#v", items[0])
	}

	items, err = store.SearchDocumentChunks(domain.DocumentSearch{
		Query:             "deployment password",
		Embedding:         []float64{1, 0},
		EmbeddingProvider: "openai_compatible",
		EmbeddingModel:    "text-embedding-3-large",
		Metadata:          map[string]string{"project": "agentflow"},
		Limit:             3,
	})
	if err != nil {
		t.Fatalf("search document chunks with embedding model filter: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected embedding model mismatch to filter chunks, got %d", len(items))
	}

	items, err = store.SearchDocumentChunks(domain.DocumentSearch{
		Query:         "deployment password",
		Embedding:     []float64{0, 1},
		Metadata:      map[string]string{"project": "agentflow"},
		Limit:         3,
		MinSimilarity: 0.9,
	})
	if err != nil {
		t.Fatalf("search document chunks with threshold: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected high threshold to filter mismatched chunks, got %d", len(items))
	}
}

func TestFileStorePersistsDocumentChunkSourceDetails(t *testing.T) {
	path := t.TempDir() + "/agentflow.json"
	fileStore, err := NewFileStore(path)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	content := "Deploy AgentFlow from the release guide."
	document, err := fileStore.CreateDocument(domain.Document{
		Title: "Release guide", Version: "release-v3", ContentHash: "document-hash", SourceType: "text", Content: content,
	}, []domain.DocumentChunk{{
		ChunkSource: domain.ChunkSource{ParentID: "parent-release", SectionPath: []string{"Release", "Deploy"}, StartOffset: 0, EndOffset: len(content),
			DocumentVersion: "release-v3", ContentHash: "chunk-hash"}, Content: content, TokenCount: 10,
	}}, []domain.DocumentChunkEmbedding{{Provider: "test", Model: "test", Dimensions: 2, Embedding: []float64{1, 0}}})
	if err != nil {
		t.Fatalf("create document: %v", err)
	}

	reloaded, err := NewFileStore(path)
	if err != nil {
		t.Fatalf("reload store: %v", err)
	}
	loadedDocument, chunks, found, err := reloaded.GetDocument(document.ID)
	if err != nil || !found || len(chunks) != 1 {
		t.Fatalf("get reloaded document: found=%v chunks=%d err=%v", found, len(chunks), err)
	}
	chunk := chunks[0]
	if loadedDocument.Content != content || loadedDocument.Version != "release-v3" || loadedDocument.ContentHash != "document-hash" || chunk.ParentID != "parent-release" || chunk.DocumentVersion != "release-v3" || chunk.ContentHash != "chunk-hash" || chunk.StartOffset != 0 || chunk.EndOffset != len(content) || strings.Join(chunk.SectionPath, " > ") != "Release > Deploy" {
		t.Fatalf("unexpected persisted source details: document=%#v chunk=%#v", loadedDocument, chunk)
	}
	if loadedDocument.Content[chunk.StartOffset:chunk.EndOffset] != chunk.Content {
		t.Fatalf("persisted offsets did not resolve to source content")
	}
}

func TestFileStoreDocumentSearchSupportsExpandedCandidateLimit(t *testing.T) {
	store, err := NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	chunks := make([]domain.DocumentChunk, 12)
	embeddings := make([]domain.DocumentChunkEmbedding, 12)
	for index := range chunks {
		chunks[index] = domain.DocumentChunk{Content: fmt.Sprintf("Candidate %d", index), TokenCount: 2}
		embeddings[index] = domain.DocumentChunkEmbedding{Embedding: []float64{1, 0}}
	}
	if _, err := store.CreateDocument(domain.Document{
		Title: "Candidate set", SourceType: "text", Content: "Candidate set",
	}, chunks, embeddings); err != nil {
		t.Fatalf("create document: %v", err)
	}

	items, err := store.SearchDocumentChunks(domain.DocumentSearch{Embedding: []float64{1, 0}, Limit: 12})
	if err != nil {
		t.Fatalf("search document chunks: %v", err)
	}
	if len(items) != 12 {
		t.Fatalf("expected expanded candidate limit to return 12 chunks, got %d", len(items))
	}
}

func TestFileStoreLexicalRecallFindsExactIdentifierWithFilters(t *testing.T) {
	store, err := NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	for _, workspaceID := range []string{"workspace-a", "workspace-b"} {
		content := "AUTH-7F31 means the refresh token has expired."
		if _, err := store.CreateDocument(domain.Document{
			WorkspaceID: workspaceID,
			Title:       "Authentication error catalog",
			SourceType:  "text",
			Content:     content,
			Metadata:    map[string]any{"project": "agentflow"},
		}, []domain.DocumentChunk{{
			Content: content, TokenCount: 10, Metadata: map[string]any{"project": "agentflow"},
		}}, []domain.DocumentChunkEmbedding{{Embedding: []float64{1, 0}}}); err != nil {
			t.Fatalf("create document: %v", err)
		}
	}

	items, err := store.SearchDocumentChunksLexical(domain.DocumentSearch{
		Query:        "AUTH-7F31 怎么解决",
		LexicalTerms: []string{"auth", "7f31", "怎么", "解决"},
		WorkspaceID:  "workspace-a",
		Metadata:     map[string]string{"project": "agentflow"},
		Limit:        5,
	})
	if err != nil {
		t.Fatalf("search document chunks lexically: %v", err)
	}
	if len(items) != 1 || items[0].Document.WorkspaceID != "workspace-a" {
		t.Fatalf("expected one workspace-filtered lexical result, got %#v", items)
	}
	if items[0].LexicalScore != 1 || items[0].Similarity != 0 {
		t.Fatalf("expected exact lexical score without vector similarity, got %#v", items[0])
	}
}
