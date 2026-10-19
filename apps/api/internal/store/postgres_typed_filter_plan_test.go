package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/identity"
	"agentflow-platform/apps/api/internal/testsupport/pgfixture"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// Capture actual production SQL without adding a query/debug surface to Store.
// Failure inventory: selective/empty filters underfill, post-ANN filtering loses
// eligible rows, unsafe casts or interpolated input, plans measured on another query.
func TestPostgresTypedFilterPlans(t *testing.T) {
	url := pgfixture.DatabaseURL(t)
	s, err := NewPostgresStore(url)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	trace := &typedFilterQueryTrace{}
	config, err := pgx.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	config.Tracer = trace
	old := s.db
	s.db = stdlib.OpenDB(*config)
	_ = old.Close()
	w, err := s.CreateWorkspace(t.Context(), identity.SuperUserID, "Typed query plans", "")
	if err != nil {
		t.Fatal(err)
	}
	vector := make([]float64, 1536)
	vector[0] = 1
	chunks := make([]domain.DocumentChunk, 500)
	embeddings := make([]domain.DocumentChunkEmbedding, 500)
	for i := range chunks {
		priority := 0
		if i < 250 {
			priority = 50
		}
		if i < 5 {
			priority = 99
		}
		v := append([]float64(nil), vector...)
		if i < 5 {
			v[0], v[1] = 0.6, 0.8
		}
		chunks[i] = domain.DocumentChunk{ID: fmt.Sprintf("plan-chunk-%d", i), Content: "ROLLBACK-42 production recovery", Metadata: map[string]any{"priority": priority}}
		embeddings[i] = domain.DocumentChunkEmbedding{Provider: "test", Model: "plan-vector", Embedding: v}
	}
	if _, err := s.CreateDocument(domain.Document{ID: "plan-document", WorkspaceID: w.ID, Title: "ROLLBACK-42", Content: "ROLLBACK-42", MetadataSchemaVersion: "knowledge-metadata-v1"}, chunks, embeddings); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("ANALYZE documents; ANALYZE document_chunks; ANALYZE document_chunk_embeddings"); err != nil {
		t.Fatal(err)
	}
	var postgres, pgvector string
	if err := s.db.QueryRow("SHOW server_version").Scan(&postgres); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow("SELECT extversion FROM pg_extension WHERE extname='vector'").Scan(&pgvector); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name                string
		threshold, eligible int
	}{{"unfiltered", -1, 500}, {"half", 50, 250}, {"one_percent", 99, 5}, {"none", 100, 0}} {
		search := domain.DocumentSearch{Query: "ROLLBACK-42", WorkspaceID: w.ID, LexicalTerms: []string{"rollback", "42"}, Embedding: vector, Limit: 20}
		if tc.threshold >= 0 {
			search.MetadataFilter = &domain.MetadataFilter{SchemaVersion: "knowledge-metadata-v1", Where: domain.MetadataCondition{Field: "chunk.metadata.priority", Op: "gte", Value: tc.threshold}}
		}
		for path, recall := range map[string]func(domain.DocumentSearch) ([]domain.RetrievedDocumentChunk, error){"semantic": s.SearchDocumentChunks, "lexical": s.SearchDocumentChunksLexical} {
			latencies := []int64{}
			for range 3 {
				start := time.Now()
				items, err := recall(search)
				latencies = append(latencies, time.Since(start).Nanoseconds())
				if err != nil || len(items) != min(tc.eligible, 20) {
					t.Fatalf("%s/%s returned=%d eligible=%d err=%v", tc.name, path, len(items), tc.eligible, err)
				}
			}
			query, args := trace.query, trace.args
			var plan json.RawMessage
			if err := s.db.QueryRow("EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) "+query, args...).Scan(&plan); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(plan), "idx_document_chunk_embeddings_vector") {
				t.Fatal("approximate index used before selective eligibility")
			}
			evidence, _ := json.Marshal(map[string]any{"schema": "typed-metadata-plan-v1", "case": tc.name, "path": path, "postgres": postgres, "pgvector": pgvector, "workspace": w.ID, "corpus_chunks": 500, "eligible": tc.eligible, "requested": 20, "expected_returned": min(tc.eligible, 20), "filter": search.MetadataFilter, "query": query, "args": args, "elapsed_ns": latencies, "explain": plan, "limitations": "synthetic corpus, 3 local samples; not calibrated p95 or scale evidence; no new index justified by this sample"})
			t.Logf("typed-filter-plan-evidence %s", evidence)
		}
	}
}

type typedFilterQueryTrace struct {
	query string
	args  []any
}

func (t *typedFilterQueryTrace) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if strings.Contains(data.SQL, "AS score") && !strings.HasPrefix(data.SQL, "EXPLAIN") {
		t.query = data.SQL
		t.args = append([]any(nil), data.Args...)
	}
	return ctx
}
func (*typedFilterQueryTrace) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}
