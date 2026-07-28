package store

import (
	"fmt"
	"os"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
)

func TestFileStoreDocumentStoreContract(t *testing.T) {
	fileStore, err := NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new file store: %v", err)
	}
	runDocumentStoreContract(t, fileStore)
}

func TestPostgresStoreDocumentStoreContract(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	postgresStore, err := NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatalf("new postgres store: %v", err)
	}
	defer postgresStore.Close()
	runDocumentStoreContract(t, postgresStore)
}

func runDocumentStoreContract(t *testing.T, documentStore DocumentStore) {
	t.Helper()
	suffix := time.Now().UTC().UnixNano()
	documentID := fmt.Sprintf("doc_contract_%d", suffix)
	chunkID := fmt.Sprintf("chunk_contract_%d", suffix)
	workspaceID := fmt.Sprintf("workspace_contract_%d", suffix)
	embedding := make([]float64, 1536)
	embedding[0] = 1

	document, err := documentStore.CreateDocument(domain.Document{
		ID:          documentID,
		WorkspaceID: workspaceID,
		Title:       "Authentication error catalog",
		Version:     "2026-07",
		ContentHash: "sha256:document-contract",
		SourceType:  "markdown",
		SourceURI:   "auth-errors.md",
		MimeType:    "text/markdown",
		Content:     "# Authentication\n\nAUTH-7F31 means the refresh token has expired.",
		Metadata:    map[string]any{"project": "agentflow"},
	}, []domain.DocumentChunk{{
		ID: chunkID,
		ChunkSource: domain.ChunkSource{
			ParentID:        "section_authentication",
			SectionPath:     []string{"Authentication"},
			StartOffset:     18,
			EndOffset:       66,
			DocumentVersion: "2026-07",
			ContentHash:     "sha256:chunk-contract",
		},
		Content:    "AUTH-7F31 means the refresh token has expired.",
		TokenCount: 10,
		Metadata:   map[string]any{"project": "agentflow", "chunk_type": "paragraph"},
	}}, []domain.DocumentChunkEmbedding{{
		Provider:   "contract",
		Model:      "contract-1536",
		Dimensions: 1536,
		Embedding:  embedding,
	}})
	if err != nil {
		t.Fatalf("create document: %v", err)
	}
	t.Cleanup(func() { _ = documentStore.DeleteDocument(documentID) })
	if document.ID != documentID || document.ChunkCount != 1 || document.EmbeddingCount != 1 {
		t.Fatalf("unexpected created document: %#v", document)
	}

	documents, err := documentStore.ListDocuments()
	if err != nil {
		t.Fatalf("list documents: %v", err)
	}
	if !containsDocument(documents, documentID) {
		t.Fatalf("created document %q missing from list", documentID)
	}

	loaded, chunks, found, err := documentStore.GetDocument(documentID)
	if err != nil || !found {
		t.Fatalf("get document: found=%v err=%v", found, err)
	}
	if loaded.Version != document.Version || loaded.ContentHash != document.ContentHash || len(chunks) != 1 {
		t.Fatalf("document source details did not round trip: document=%#v chunks=%#v", loaded, chunks)
	}
	chunk := chunks[0]
	if chunk.ParentID != "section_authentication" || len(chunk.SectionPath) != 1 ||
		chunk.SectionPath[0] != "Authentication" || chunk.StartOffset != 18 || chunk.EndOffset != 66 ||
		chunk.DocumentVersion != "2026-07" || chunk.ContentHash != "sha256:chunk-contract" {
		t.Fatalf("chunk source details did not round trip: %#v", chunk)
	}

	semanticResults, err := documentStore.SearchDocumentChunks(domain.DocumentSearch{
		Query:             "refresh token expired",
		Embedding:         embedding,
		EmbeddingProvider: "contract",
		EmbeddingModel:    "contract-1536",
		WorkspaceID:       workspaceID,
		Metadata:          map[string]string{"project": "agentflow"},
		Limit:             5,
		MinSimilarity:     0.9,
	})
	if err != nil {
		t.Fatalf("semantic search: %v", err)
	}
	if len(semanticResults) != 1 || semanticResults[0].Chunk.ID != chunkID {
		t.Fatalf("unexpected semantic results: %#v", semanticResults)
	}

	lexicalResults, err := documentStore.SearchDocumentChunksLexical(domain.DocumentSearch{
		Query:        "AUTH-7F31",
		LexicalTerms: []string{"auth", "7f31"},
		WorkspaceID:  workspaceID,
		Metadata:     map[string]string{"project": "agentflow"},
		Limit:        5,
	})
	if err != nil {
		t.Fatalf("lexical search: %v", err)
	}
	if len(lexicalResults) != 1 || lexicalResults[0].Chunk.ID != chunkID || lexicalResults[0].LexicalScore <= 0 {
		t.Fatalf("unexpected lexical results: %#v", lexicalResults)
	}

	if err := documentStore.DeleteDocument(documentID); err != nil {
		t.Fatalf("delete document: %v", err)
	}
	if _, _, found, err := documentStore.GetDocument(documentID); err != nil || found {
		t.Fatalf("deleted document still available: found=%v err=%v", found, err)
	}
}

func containsDocument(documents []domain.Document, documentID string) bool {
	for _, document := range documents {
		if document.ID == documentID {
			return true
		}
	}
	return false
}
