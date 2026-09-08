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
	if document.SourceKey != "auth-errors.md" || document.IndexIdentity != (domain.DocumentIndexIdentity{
		ChunkerVersion: domain.DocumentChunkerVersion, EmbeddingProvider: "contract", EmbeddingModel: "contract-1536", EmbeddingDimensions: 1536,
	}) {
		t.Fatalf("document index identity was not bound: %#v", document)
	}

	duplicate, err := documentStore.CreateDocument(domain.Document{
		WorkspaceID: workspaceID, Title: document.Title, Version: document.Version, ContentHash: document.ContentHash,
		SourceType: document.SourceType, SourceURI: document.SourceURI, MimeType: document.MimeType,
		Content: "# Authentication\n\nAUTH-7F31 means the refresh token has expired.", Metadata: map[string]any{"project": "agentflow"},
	}, []domain.DocumentChunk{{
		ChunkSource: domain.ChunkSource{ParentID: "section_authentication", SectionPath: []string{"Authentication"}, StartOffset: 18, EndOffset: 66, DocumentVersion: "2026-07", ContentHash: "sha256:chunk-contract"},
		Content:     "AUTH-7F31 means the refresh token has expired.", TokenCount: 10, Metadata: map[string]any{"project": "agentflow", "chunk_type": "paragraph"},
	}}, []domain.DocumentChunkEmbedding{{Provider: "contract", Model: "contract-1536", Dimensions: 1536, Embedding: embedding}})
	if err != nil || duplicate.ID != document.ID || !duplicate.UpdatedAt.Equal(document.UpdatedAt) {
		t.Fatalf("identical ingest was not idempotent: document=%#v err=%v", duplicate, err)
	}

	_, err = documentStore.CreateDocument(domain.Document{
		WorkspaceID: workspaceID, Title: document.Title, Version: document.Version, ContentHash: "sha256:different-content",
		SourceType: document.SourceType, SourceURI: document.SourceURI, Content: "different content",
	}, []domain.DocumentChunk{{Content: "different content"}}, []domain.DocumentChunkEmbedding{{Provider: "contract", Model: "contract-1536", Dimensions: 1536, Embedding: embedding}})
	if !IsDocumentVersionConflict(err) {
		t.Fatalf("same source/version accepted different content: %v", err)
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

	contextResults, err := documentStore.ListDocumentContextChunks(domain.DocumentContextSearch{
		DocumentID:     documentID,
		WorkspaceID:    workspaceID,
		ParentID:       "section_authentication",
		ChunkIndex:     0,
		NeighborWindow: 1,
		Metadata:       map[string]string{"project": "agentflow"},
	})
	if err != nil {
		t.Fatalf("list document context: %v", err)
	}
	if len(contextResults) != 1 || contextResults[0].Chunk.ID != chunkID || contextResults[0].Document.WorkspaceID != workspaceID {
		t.Fatalf("unexpected scoped context results: %#v", contextResults)
	}
	crossWorkspaceResults, err := documentStore.ListDocumentContextChunks(domain.DocumentContextSearch{
		DocumentID:     documentID,
		WorkspaceID:    "another-workspace",
		ParentID:       "section_authentication",
		ChunkIndex:     0,
		NeighborWindow: 1,
	})
	if err != nil {
		t.Fatalf("list cross-workspace context: %v", err)
	}
	if len(crossWorkspaceResults) != 0 {
		t.Fatalf("expected workspace scope to block context expansion, got %#v", crossWorkspaceResults)
	}

	updatedContent := "AUTH-8G42 means the session signing key has expired."
	updated, err := documentStore.CreateDocument(domain.Document{
		WorkspaceID: workspaceID, Title: "Authentication error catalog", Version: "2026-08", ContentHash: "sha256:document-contract-v2",
		SourceType: "markdown", SourceURI: "auth-errors.md", MimeType: "text/markdown", Content: updatedContent,
		Metadata: map[string]any{"project": "agentflow"},
	}, []domain.DocumentChunk{{
		ChunkSource: domain.ChunkSource{ParentID: "section_authentication_v2", SectionPath: []string{"Authentication"}, StartOffset: 0, EndOffset: len(updatedContent), DocumentVersion: "2026-08", ContentHash: "sha256:chunk-contract-v2"},
		Content:     updatedContent, TokenCount: 11, Metadata: map[string]any{"project": "agentflow", "chunk_type": "paragraph"},
	}}, []domain.DocumentChunkEmbedding{{Provider: "contract", Model: "contract-1536", Dimensions: 1536, Embedding: embedding}})
	if err != nil || updated.ID != document.ID || updated.Version != "2026-08" || !updated.CreatedAt.Equal(document.CreatedAt) {
		t.Fatalf("new source version was not atomically installed: document=%#v err=%v", updated, err)
	}
	loaded, chunks, found, err = documentStore.GetDocument(document.ID)
	if err != nil || !found || loaded.Version != "2026-08" || len(chunks) != 1 || chunks[0].ID == chunkID || chunks[0].DocumentVersion != "2026-08" {
		t.Fatalf("old version reference still resolves to stale index data: document=%#v chunks=%#v found=%v err=%v", loaded, chunks, found, err)
	}
	stale, err := documentStore.SearchDocumentChunksLexical(domain.DocumentSearch{
		Query: "AUTH-7F31", LexicalTerms: []string{"7f31"}, WorkspaceID: workspaceID, Limit: 5,
	})
	if err != nil || len(stale) != 0 {
		t.Fatalf("old source version remained in recall: results=%#v err=%v", stale, err)
	}
	documents, err = documentStore.ListDocumentsByWorkspace(workspaceID)
	if err != nil || len(documents) != 1 || documents[0].ID != document.ID || documents[0].Version != "2026-08" {
		t.Fatalf("source replacement created duplicate active documents: %#v err=%v", documents, err)
	}

	if err := documentStore.DeleteDocument(documentID); err != nil {
		t.Fatalf("delete document: %v", err)
	}
	if _, _, found, err := documentStore.GetDocument(documentID); err != nil || found {
		t.Fatalf("deleted document still available: found=%v err=%v", found, err)
	}
	identities, err := documentStore.ListDocumentIndexIdentities(workspaceID)
	if err != nil || len(identities) != 0 {
		t.Fatalf("deleted document left index identity state: %#v err=%v", identities, err)
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
