package store_test

import (
	"database/sql"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/identity"
	"agentflow-platform/apps/api/internal/rag"
	"agentflow-platform/apps/api/internal/rag/eligibility"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/testsupport/pgfixture"
)

// Failure inventory: startup must upgrade old databases idempotently; restarted
// reads retain lifecycle without reindexing. Dense/lexical/expansion must apply
// the mandatory gate before LIMIT, including an unrestricted user OR filter.
func TestPostgresDocumentLifecycleMigrationAndRecall(t *testing.T) {
	url := pgfixture.DatabaseURL(t)
	s, err := store.NewPostgresStore(url)
	if err != nil {
		t.Fatal(err)
	}
	w, err := s.CreateWorkspace(t.Context(), identity.SuperUserID, "Lifecycle store", "")
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	legacyVector := make([]float64, 1536)
	legacyVector[0] = 1
	legacy, err := s.CreateDocument(domain.Document{WorkspaceID: w.ID, Title: "Legacy labels", Content: "Legacy guide.", Metadata: map[string]any{"status": "withdrawn", "valid_until": "2000-01-01T00:00:00Z"}}, []domain.DocumentChunk{{Content: "Legacy guide."}}, []domain.DocumentChunkEmbedding{{Embedding: legacyVector}})
	if err != nil {
		t.Fatal(err)
	}
	// Simulate the previous schema; migration must create exactly the column
	// consumed by INSERT/SELECT/UPDATE, without certifying old metadata labels.
	if _, err := db.Exec(`ALTER TABLE documents DROP COLUMN lifecycle`); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = store.NewPostgresStore(url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	upgraded, _, ok, err := s.GetDocument(legacy.ID)
	if err != nil || !ok || upgraded.Lifecycle.Status != "active" || upgraded.Lifecycle.ValidUntil != "" || upgraded.Metadata["status"] != "withdrawn" {
		t.Fatalf("migration reinterpreted legacy labels: %+v %v", upgraded, err)
	}
	if err := s.DeleteDocument(legacy.ID); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 24; i++ {
		status := "withdrawn"
		if i == 0 {
			status = "active"
		}
		doc, chunks, err := rag.BuildDocument(domain.DocumentIngestRequest{WorkspaceID: w.ID, Title: "ROLLBACK-42", Content: "ROLLBACK-42 durable checkpoints.", MetadataSchemaVersion: "knowledge-metadata-v1", Metadata: map[string]any{"product": "AgentFlow"}, Lifecycle: &domain.DocumentLifecycle{Status: status}})
		if err != nil {
			t.Fatal(err)
		}
		vector := make([]float64, 1536)
		vector[0] = 1
		if _, err := s.CreateDocument(doc, chunks, []domain.DocumentChunkEmbedding{{Provider: "test", Model: "test", Dimensions: 1536, Embedding: vector}}); err != nil {
			t.Fatal(err)
		}
	}
	docs, err := s.ListDocumentsByWorkspace(w.ID)
	if err != nil {
		t.Fatal(err)
	}
	var active domain.Document
	for _, d := range docs {
		if d.Lifecycle.Status == "active" {
			active = d
		}
	}
	if active.ID == "" {
		t.Fatal("missing lifecycle on list")
	}
	var chunks []domain.DocumentChunk
	_, chunks, _, err = s.GetDocument(active.ID)
	if err != nil {
		t.Fatal(err)
	}
	vector := make([]float64, 1536)
	vector[0] = 1
	for _, filter := range []*domain.MetadataFilter{nil, {SchemaVersion: "knowledge-metadata-v1", Where: domain.MetadataCondition{Any: []domain.MetadataCondition{{Field: "document.metadata.product", Op: "eq", Value: "AgentFlow"}, {Field: "chunk.metadata.product", Op: "eq", Value: "Other"}}}}} {
		search := domain.DocumentSearch{WorkspaceID: w.ID, Query: "ROLLBACK-42", LexicalTerms: []string{"rollback"}, Embedding: vector, Limit: 1, MetadataFilter: filter}
		for _, recall := range []func(domain.DocumentSearch) ([]domain.RetrievedDocumentChunk, error){s.SearchDocumentChunks, s.SearchDocumentChunksLexical} {
			items, err := recall(search)
			if err != nil || len(items) != 1 || items[0].Document.ID != active.ID {
				t.Fatalf("pre-limit eligibility: %v %v", items, err)
			}
		}
	}
	// Fixed-clock SQL and in-memory predicates agree at exact microsecond bounds.
	at := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	for _, value := range []domain.DocumentLifecycle{
		{}, {ValidFrom: at.Format(time.RFC3339)}, {ValidUntil: at.Format(time.RFC3339)},
		{ValidUntil: at.Add(time.Microsecond).Format(time.RFC3339Nano)}, {Status: "draft"},
	} {
		active, err = s.UpdateDocumentMetadata(w.ID, active.ID, domain.DocumentMetadataUpdate{ExpectedUpdatedAt: active.UpdatedAt, Metadata: active.Metadata, Lifecycle: &value})
		if err != nil {
			t.Fatal(err)
		}
		predicate, err := eligibility.CompileAt(nil, at)
		if err != nil {
			t.Fatal(err)
		}
		args := []any{active.ID}
		where := predicate.SQL(&args)
		var got bool
		if err := db.QueryRow(`SELECT `+where+` FROM documents d WHERE d.id=$1`, args...).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != predicate.Matches(active, chunks[0]) {
			t.Fatalf("SQL/in-memory boundary mismatch: %+v", value)
		}
	}
	active, err = s.UpdateDocumentMetadata(w.ID, active.ID, domain.DocumentMetadataUpdate{ExpectedUpdatedAt: active.UpdatedAt, Metadata: active.Metadata, Lifecycle: &domain.DocumentLifecycle{Status: "withdrawn"}})
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = store.NewPostgresStore(url)
	if err != nil {
		t.Fatal(err)
	}
	loaded, _, ok, err := s.GetDocument(active.ID)
	if err != nil || !ok || loaded.Lifecycle != active.Lifecycle || loaded.EmbeddingCount != active.EmbeddingCount {
		t.Fatalf("restart lost lifecycle/index: %+v %v", loaded, err)
	}
	expanded, err := s.ListDocumentContextChunks(domain.DocumentContextSearch{DocumentID: active.ID, WorkspaceID: w.ID, ChunkIndex: 0, NeighborWindow: 1})
	if err != nil || len(expanded) != 0 {
		t.Fatalf("withdrawn expansion: %v %v", expanded, err)
	}
	// A corrupt persisted null must not become an omitted/active field on a
	// direct Tool read, while SQL already excludes it from recall.
	if _, err := db.Exec(`UPDATE documents SET lifecycle='{"status":null}' WHERE id=$1`, active.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.GetDocument(active.ID); err == nil {
		t.Fatal("corrupt lifecycle decoded as active")
	}
}
