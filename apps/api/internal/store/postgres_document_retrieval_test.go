package store

import (
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/testsupport/pgfixture"
	"strings"
	"sync"
	"testing"
)

func TestPostgresStoreLexicalRecall(t *testing.T) {
	databaseURL := pgfixture.DatabaseURL(t)
	store, err := NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatalf("new postgres store: %v", err)
	}
	defer store.Close()

	workspaceID := testOwnedWorkspace(t, store)
	content := "AUTH-7F31 means the refresh token has expired."
	embedding := make([]float64, 1536)
	embedding[0] = 1
	document, err := store.CreateDocument(domain.Document{
		WorkspaceID: workspaceID, Title: "Authentication errors", Version: "auth-v1", ContentHash: "document-hash", SourceType: "text", Content: content,
	}, []domain.DocumentChunk{{ChunkSource: domain.ChunkSource{ParentID: "parent-auth", SectionPath: []string{"Errors"}, StartOffset: 0, EndOffset: len(content), DocumentVersion: "auth-v1", ContentHash: "chunk-hash"}, Content: content, TokenCount: 10}}, []domain.DocumentChunkEmbedding{{
		Provider: "test", Model: "embedding-v1", Dimensions: 1536, Embedding: embedding,
	}})
	if err != nil {
		t.Fatalf("create document: %v", err)
	}
	t.Cleanup(func() { _ = store.DeleteDocument(document.ID) })

	items, err := store.SearchDocumentChunksLexical(domain.DocumentSearch{
		Query: "AUTH-7F31 怎么解决", LexicalTerms: []string{"auth", "7f31", "怎么", "解决"},
		WorkspaceID: workspaceID, Limit: 5,
	})
	if err != nil {
		t.Fatalf("search document chunks lexically: %v", err)
	}
	if len(items) != 1 || items[0].Document.ID != document.ID || items[0].LexicalScore <= 0 {
		t.Fatalf("expected postgres lexical match, got %#v", items)
	}
	if items[0].Document.Version != "auth-v1" || items[0].Chunk.ParentID != "parent-auth" || items[0].Chunk.ContentHash != "chunk-hash" || strings.Join(items[0].Chunk.SectionPath, " > ") != "Errors" {
		t.Fatalf("expected postgres source details round trip, got %#v", items[0])
	}
}

func TestPostgresConcurrentIdenticalDocumentIngestKeepsOneActiveIndex(t *testing.T) {
	databaseURL := pgfixture.DatabaseURL(t)
	store, err := NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatalf("new postgres store: %v", err)
	}
	defer store.Close()
	workspaceID := testOwnedWorkspace(t, store)
	embedding := make([]float64, 1536)
	embedding[0] = 1
	var wait sync.WaitGroup
	failures := make(chan error, 8)
	for index := 0; index < 8; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, err := store.CreateDocument(domain.Document{
				WorkspaceID: workspaceID, SourceKey: "refund-policy", Title: "Refund policy", Version: "3.2",
				ContentHash: "refund-v3-hash", SourceType: "markdown", SourceURI: "refund-current.md", Content: "Refunds are automatic within 14 days.",
			}, []domain.DocumentChunk{{Content: "Refunds are automatic within 14 days."}}, []domain.DocumentChunkEmbedding{{Provider: "test", Model: "embedding-v1", Dimensions: 1536, Embedding: embedding}})
			if err != nil {
				failures <- err
			}
		}()
	}
	wait.Wait()
	close(failures)
	for err := range failures {
		t.Errorf("concurrent ingest: %v", err)
	}
	documents, err := store.ListDocumentsByWorkspace(workspaceID)
	if err != nil || len(documents) != 1 || documents[0].Version != "3.2" || documents[0].ChunkCount != 1 || documents[0].EmbeddingCount != 1 {
		t.Fatalf("concurrent ingest produced duplicate or partial index: documents=%#v err=%v", documents, err)
	}
	if len(documents) == 1 {
		t.Cleanup(func() { _ = store.DeleteDocument(documents[0].ID) })
	}
}
