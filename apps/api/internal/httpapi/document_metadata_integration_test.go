package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/identity"
	"agentflow-platform/apps/api/internal/inference/provider"
	"agentflow-platform/apps/api/internal/knowledge"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/testsupport/fixturestore"
	"agentflow-platform/apps/api/internal/testsupport/pgfixture"
)

type metadataEmbedder struct{ calls atomic.Int64 }

// Schedule a real reindex after the ingest lookup but before its mutation. The
// underlying Store and its locks/validation are not replaced by a success stub.
type metadataReindexRaceStore struct {
	store.Store
	reindex func()
}

func (s metadataReindexRaceStore) GetDocumentBySource(workspace, source string) (domain.Document, bool, error) {
	document, found, err := s.Store.GetDocumentBySource(workspace, source)
	if err == nil && found {
		s.reindex()
	}
	return document, found, err
}

func (e *metadataEmbedder) EmbedText(context.Context, string) (provider.Embedding, error) {
	e.calls.Add(1)
	vector := make([]float64, 1536)
	vector[0] = 1
	return provider.Embedding{Vector: vector, Provider: "metadata-test", Model: "metadata-1536", Dimensions: 1536}, nil
}

// HTTP -> real KnowledgeBase -> Store: only the external embedding provider is
// controlled. The failure inventory is maintained in metadata-updates.md.
func TestDocumentMetadataUpdates(t *testing.T) {
	t.Run("fixture", func(t *testing.T) { runMetadataHTTPContract(t, fixturestore.New(), "1", "2", "") })
	t.Run("postgres", func(t *testing.T) {
		url := pgfixture.DatabaseURL(t)
		s, err := store.NewPostgresStore(url)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		workspace, err := s.CreateWorkspace(t.Context(), identity.SuperUserID, "Metadata updates", "")
		if err != nil {
			t.Fatal(err)
		}
		foreign, err := s.CreateWorkspace(t.Context(), identity.SuperUserID, "Foreign", "")
		if err != nil {
			t.Fatal(err)
		}
		runMetadataHTTPContract(t, s, workspace.ID, foreign.ID, url)
	})
}

func runMetadataHTTPContract(t *testing.T, backend store.Store, workspace, foreign, databaseURL string) {
	t.Helper()
	embedder := &metadataEmbedder{}
	h := &Handler{store: backend, knowledge: knowledge.NewKnowledgeBase(backend, embedder)}
	mux := http.NewServeMux()
	h.registerDocumentRoutes(mux)
	server := httptest.NewServer(mux)
	defer server.Close()
	call := func(method, path, scope string, body any) (int, []byte) {
		data, _ := json.Marshal(body)
		req, _ := http.NewRequest(method, server.URL+path, bytes.NewReader(data))
		req.Header.Set("X-Workspace-ID", scope)
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Error(err)
			return 0, nil
		}
		defer response.Body.Close()
		var raw json.RawMessage
		if err := json.NewDecoder(response.Body).Decode(&raw); err != nil {
			t.Error(err)
		}
		return response.StatusCode, raw
	}
	decode := func(data []byte) domain.Document {
		t.Helper()
		var d domain.Document
		if err := json.Unmarshal(data, &d); err != nil {
			t.Fatal(err)
		}
		return d
	}
	input := map[string]any{"source_key": "release-guide", "title": "Release guide", "source_type": "markdown", "content": "# Release\n\nDeploy AgentFlow production release.\n\n## Recovery\n\nRollback AgentFlow production release.", "metadata": map[string]any{"environment": "staging", "product": "AgentFlow"}}
	status, data := call("POST", "/api/documents", workspace, input)
	if status != 201 {
		t.Fatalf("create: %d %s", status, data)
	}
	original := decode(data)
	_, beforeChunks, _, _ := backend.GetDocumentInWorkspace(workspace, original.ID)
	// The Store must enforce the same rule even if a caller bypasses the
	// KnowledgeBase preflight (including an ingest racing the metadata update).
	direct := original
	direct.Content = input["content"].(string)
	direct.Metadata = map[string]any{"format": "markdown", "environment": "changed", "product": "AgentFlow"}
	indexEmbeddings := make([]domain.DocumentChunkEmbedding, len(beforeChunks))
	vector := make([]float64, 1536)
	vector[0] = 1
	for i := range indexEmbeddings {
		indexEmbeddings[i] = domain.DocumentChunkEmbedding{Provider: original.IndexIdentity.EmbeddingProvider, Model: original.IndexIdentity.EmbeddingModel, Embedding: vector}
	}
	if _, err := backend.CreateDocument(direct, beforeChunks, indexEmbeddings); !errors.Is(err, store.ErrDocumentMetadataConflict) {
		t.Fatalf("direct ingest silently accepted metadata drift: %v", err)
	}
	path := "/api/documents/" + original.ID + "/metadata"
	patch := map[string]any{"expected_updated_at": original.UpdatedAt, "metadata": map[string]any{"environment": "production", "product": "AgentFlow"}}
	calls := embedder.calls.Load()
	status, data = call("PATCH", path, workspace, patch)
	if status != 200 {
		t.Fatalf("metadata update: %d %s", status, data)
	}
	updated := decode(data)
	if !updated.UpdatedAt.After(original.UpdatedAt) || updated.ID != original.ID || updated.IndexIdentity != original.IndexIdentity || updated.ContentHash != original.ContentHash || updated.Version != original.Version {
		t.Fatalf("unstable update: %#v", updated)
	}
	status, data = call("PATCH", path, workspace, patch)
	if status != 200 || !decode(data).UpdatedAt.Equal(updated.UpdatedAt) {
		t.Fatalf("retry was not idempotent: %d %s", status, data)
	}
	encoded, _ := json.Marshal(patch)
	trailing, _ := http.NewRequest("PATCH", server.URL+path, bytes.NewReader(append(encoded, []byte(" {}")...)))
	trailing.Header.Set("X-Workspace-ID", workspace)
	response, err := http.DefaultClient.Do(trailing)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 400 {
		t.Fatalf("trailing JSON accepted: %d", response.StatusCode)
	}
	_, chunks, _, err := backend.GetDocumentInWorkspace(workspace, original.ID)
	if err != nil || len(chunks) != len(beforeChunks) {
		t.Fatalf("chunks: %v", err)
	}
	for i, chunk := range chunks {
		if chunk.Metadata["environment"] != "production" || chunk.ID != beforeChunks[i].ID || chunk.Content != beforeChunks[i].Content || !reflect.DeepEqual(chunk.ChunkSource, beforeChunks[i].ChunkSource) {
			t.Fatalf("chunk changed incorrectly: %#v", chunk)
		}
	}
	for _, tc := range []struct {
		name    string
		scope   string
		payload any
		status  int
	}{
		{"stale", workspace, map[string]any{"expected_updated_at": original.UpdatedAt, "metadata": map[string]any{"environment": "old"}}, 409},
		{"missing_token", workspace, map[string]any{"metadata": map[string]any{}}, 400},
		{"null", workspace, map[string]any{"expected_updated_at": updated.UpdatedAt, "metadata": nil}, 400},
		{"protected", workspace, map[string]any{"expected_updated_at": updated.UpdatedAt, "metadata": map[string]any{"format": "text"}}, 409},
		{"protected_null", workspace, map[string]any{"expected_updated_at": updated.UpdatedAt, "metadata": map[string]any{"workspace_id": nil}}, 409},
		{"array", workspace, map[string]any{"expected_updated_at": updated.UpdatedAt, "metadata": []any{}}, 400},
		{"timestamp_precision", workspace, map[string]any{"expected_updated_at": "2026-01-01T00:00:00.000000001Z", "metadata": map[string]any{}}, 400},
		{"credential", workspace, map[string]any{"expected_updated_at": updated.UpdatedAt, "metadata": map[string]any{"api_key": "not-a-real-credential"}}, 400},
		{"numeric_range", workspace, map[string]any{"expected_updated_at": updated.UpdatedAt, "metadata": map[string]any{"ratio": json.Number("1e200000")}}, 400},
		{"numeric_expansion_limit", workspace, map[string]any{"expected_updated_at": updated.UpdatedAt, "metadata": map[string]any{"ratio": json.Number("1e70000")}}, 400},
		{"large", workspace, map[string]any{"expected_updated_at": updated.UpdatedAt, "metadata": map[string]any{"large": strings.Repeat("x", 70000)}}, 400},
		{"foreign", foreign, patch, 404},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, data := call("PATCH", path, tc.scope, tc.payload)
			if status != tc.status {
				t.Fatalf("%d %s", status, data)
			}
		})
	}
	if embedder.calls.Load() != calls {
		t.Fatal("metadata update called embedding provider")
	}
	status, data = call("POST", "/api/documents", workspace, input)
	if status != 409 || embedder.calls.Load() != calls {
		t.Fatalf("unconditional metadata change was not rejected before embedding: %d %s", status, data)
	}
	// RAG-042 minimum regression: both recall modes and expansion must stop
	// matching the old inherited value, not just display the new Document label.
	for _, environment := range []string{"staging", "production"} {
		search := domain.DocumentSearch{WorkspaceID: workspace, Query: "AgentFlow release", LexicalTerms: []string{"agentflow", "release"}, Embedding: vector, Metadata: map[string]string{"environment": environment}, Limit: 20}
		for name, lookup := range map[string]func(domain.DocumentSearch) ([]domain.RetrievedDocumentChunk, error){"semantic": backend.SearchDocumentChunks, "keyword": backend.SearchDocumentChunksLexical} {
			hits, err := lookup(search)
			if err != nil || (len(hits) > 0) != (environment == "production") {
				t.Fatalf("%s %s: %d %v", name, environment, len(hits), err)
			}
		}
		expanded, err := backend.ListDocumentContextChunks(domain.DocumentContextSearch{DocumentID: original.ID, WorkspaceID: workspace, ParentID: chunks[0].ParentID, Metadata: search.Metadata})
		if err != nil || (len(expanded) > 0) != (environment == "production") {
			t.Fatalf("parent filter: %v %v", expanded, err)
		}
	}
	// Concurrent different payloads cannot both commit against the same token.
	results := make(chan int, 2)
	start := make(chan struct{})
	for _, value := range []string{"production-a", "production-b"} {
		go func(value string) {
			<-start
			code, _ := call("PATCH", path, workspace, map[string]any{"expected_updated_at": updated.UpdatedAt, "metadata": map[string]any{"environment": value}})
			results <- code
		}(value)
	}
	close(start)
	a, b := <-results, <-results
	if !(a == 200 && b == 409 || a == 409 && b == 200) {
		t.Fatalf("concurrent results: %d %d", a, b)
	}
	current, currentChunks, _, _ := backend.GetDocumentInWorkspace(workspace, original.ID)
	for _, chunk := range currentChunks {
		if chunk.Metadata["environment"] != current.Metadata["environment"] {
			t.Fatal("mixed metadata after concurrency")
		}
		if _, ok := chunk.Metadata["product"]; ok {
			t.Fatal("removed inherited metadata survived")
		}
	}
	if databaseURL != "" {
		db, err := sql.Open("pgx", databaseURL)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		for _, table := range []string{"documents", "document_chunks"} {
			_, err = db.Exec(fmt.Sprintf(`CREATE FUNCTION reject_metadata_update() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture metadata write failure'; END $$; CREATE TRIGGER reject_metadata_update BEFORE UPDATE ON %s FOR EACH ROW EXECUTE FUNCTION reject_metadata_update()`, table))
			if err != nil {
				t.Fatal(err)
			}
			status, _ := call("PATCH", path, workspace, map[string]any{"expected_updated_at": current.UpdatedAt, "metadata": map[string]any{"environment": "rollback"}})
			if status != 500 {
				t.Fatalf("failed chunk write: %d", status)
			}
			after, afterChunks, _, _ := backend.GetDocumentInWorkspace(workspace, original.ID)
			if !reflect.DeepEqual(after, current) || !reflect.DeepEqual(afterChunks, currentChunks) {
				t.Fatal("transaction failed to roll back")
			}
			if _, err := db.Exec(fmt.Sprintf(`DROP TRIGGER reject_metadata_update ON %s; DROP FUNCTION reject_metadata_update()`, table)); err != nil {
				t.Fatal(err)
			}
		}
	}
	// Same-source/body conditional ingestion is the same zero-embedding mutation.
	input["expected_updated_at"], input["metadata"] = current.UpdatedAt, map[string]any{"environment": "reingested"}
	status, data = call("POST", "/api/documents", workspace, input)
	if status != 201 || decode(data).ID != original.ID || embedder.calls.Load() != calls {
		t.Fatalf("conditional ingest: %d %s calls=%d", status, data, embedder.calls.Load())
	}
	// Explicit metadata tokens cannot silently turn into content/title reindexing.
	reindexWithToken := map[string]any{}
	for key, value := range input {
		reindexWithToken[key] = value
	}
	reindexWithToken["title"] = "Conditional reindex must fail"
	status, data = call("POST", "/api/documents", workspace, reindexWithToken)
	if status != 409 || embedder.calls.Load() != calls {
		t.Fatalf("metadata token widened to reindex: %d %s", status, data)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := knowledge.NewKnowledgeBase(backend, embedder).UpdateMetadata(canceled, workspace, original.ID, domain.DocumentMetadataUpdate{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled update: %v", err)
	}
	// Title is a real lexical/ranking input; a normal reingest must not be
	// mistaken for an idempotent metadata-only edit of unchanged body bytes.
	delete(input, "expected_updated_at")
	input["title"] = "Updated release guide"
	status, data = call("POST", "/api/documents", workspace, input)
	if status != 201 || decode(data).Title != "Updated release guide" || decode(data).ID != original.ID || embedder.calls.Load() <= calls {
		t.Fatalf("reindex: %d %s", status, data)
	}
	latest, latestChunks, _, _ := backend.GetDocumentInWorkspace(workspace, original.ID)
	input["expected_updated_at"] = latest.UpdatedAt
	h.knowledge = knowledge.NewKnowledgeBase(metadataReindexRaceStore{Store: backend, reindex: func() {
		concurrent := latest
		concurrent.Title = "Concurrent retitle"
		concurrent.Content = input["content"].(string)
		embeddings := make([]domain.DocumentChunkEmbedding, len(latestChunks))
		for i := range embeddings {
			embeddings[i] = domain.DocumentChunkEmbedding{Provider: latest.IndexIdentity.EmbeddingProvider, Model: latest.IndexIdentity.EmbeddingModel, Dimensions: 1536, Embedding: vector}
		}
		if _, err := backend.CreateDocument(concurrent, latestChunks, embeddings); err != nil {
			t.Error(err)
		}
	}}, embedder)
	status, data = call("POST", "/api/documents", workspace, input)
	if status != 409 {
		t.Fatalf("reindex between lookup and mutation must conflict, even for identical labels: %d %s", status, data)
	}
	// Preserve independent Chunk overrides: Document edits must not erase a
	// different block-local value merely because the field names are equal.
	override, err := backend.CreateDocument(domain.Document{WorkspaceID: workspace, SourceKey: "chunk-local", Title: "Chunk override", Content: "Local metadata", Metadata: map[string]any{"locale": "en", "environment": "staging"}},
		[]domain.DocumentChunk{{Content: "Local metadata", Metadata: map[string]any{"locale": "fr", "environment": "staging", "chunk_type": "paragraph"}}, {Content: "Inherited metadata", Metadata: map[string]any{"locale": "en", "environment": "staging", "chunk_type": "paragraph"}}},
		[]domain.DocumentChunkEmbedding{{Embedding: vector}, {Embedding: vector}})
	if err != nil {
		t.Fatal(err)
	}
	status, data = call("PATCH", "/api/documents/"+override.ID+"/metadata", workspace, map[string]any{"expected_updated_at": override.UpdatedAt, "metadata": map[string]any{"locale": "de", "environment": "production"}})
	if status != 200 {
		t.Fatalf("override update: %d %s", status, data)
	}
	_, overrideChunks, _, _ := backend.GetDocumentInWorkspace(workspace, override.ID)
	if overrideChunks[0].Metadata["locale"] != "fr" || overrideChunks[1].Metadata["locale"] != "de" {
		t.Fatalf("Chunk override lost: %#v", overrideChunks)
	}
	numeric := map[string]any{"expected_updated_at": decode(data).UpdatedAt, "metadata": map[string]any{"ratio": json.Number("1.0e+0")}}
	status, data = call("PATCH", "/api/documents/"+override.ID+"/metadata", workspace, numeric)
	if status != 200 {
		t.Fatalf("numeric update: %d %s", status, data)
	}
	numericTime := decode(data).UpdatedAt
	status, data = call("PATCH", "/api/documents/"+override.ID+"/metadata", workspace, numeric)
	if status != 200 || !decode(data).UpdatedAt.Equal(numericTime) {
		t.Fatalf("JSONB-normalized retry: %d %s", status, data)
	}
	// A connection restart must not undo metadata or change the concurrency token.
	if databaseURL != "" {
		reopened, err := store.NewPostgresStore(databaseURL)
		if err != nil {
			t.Fatal(err)
		}
		defer reopened.Close()
		persisted, _, found, err := reopened.GetDocumentInWorkspace(workspace, original.ID)
		if err != nil || !found || persisted.Metadata["environment"] != "reingested" {
			t.Fatalf("restart: %#v %v", persisted, err)
		}
		closed := backend.(*store.PostgresStore)
		if err := closed.Close(); err != nil {
			t.Fatal(err)
		}
		status, _ := call("PATCH", path, workspace, patch)
		if status != 500 {
			t.Fatalf("closed database reported as client success/validation: %d", status)
		}
		if _, err := knowledge.NewKnowledgeBase(closed, embedder).Ingest(t.Context(), domain.DocumentIngestRequest{SourceKey: "release-guide", WorkspaceID: workspace, Title: "Release guide", Content: "Release"}); err == nil {
			t.Fatal("ingest hid a source lookup failure")
		}
	}
}
