package store_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/identity"
	"agentflow-platform/apps/api/internal/rag"
	"agentflow-platform/apps/api/internal/rag/eligibility"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/testsupport/fixturestore"
	"agentflow-platform/apps/api/internal/testsupport/pgfixture"
)

// Failure inventory: typed-metadata-filters.md. Same corpus/predicates through
// each real Store, including independent expansion and the final context recheck.
func TestTypedMetadataFilterStoreParity(t *testing.T) {
	pg, err := store.NewPostgresStore(pgfixture.DatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer pg.Close()
	w, err := pg.CreateWorkspace(t.Context(), identity.SuperUserID, "Typed parity", "")
	if err != nil {
		t.Fatal(err)
	}
	for name, backend := range map[string]metadataParityStore{"postgres": pg, "fixture": fixturestore.New()} {
		t.Run(name, func(t *testing.T) {
			metadata := map[string]any{"product": "AgentFlow", "environment": "production", "priority": json.Number("9.00"), "tags": []string{"release", "safe"}, "is_reference": false, "published_at": "2026-10-18T04:00:00.123456Z"}
			chunks := []domain.DocumentChunk{}
			embeddings := []domain.DocumentChunkEmbedding{}
			for index := range 3 {
				labels := map[string]any{"product": "AgentFlow", "priority": 9, "tags": []string{"release"}}
				if index != 1 {
					labels["product"] = "Other"
					labels["priority"] = 0
				}
				chunks = append(chunks, domain.DocumentChunk{ID: fmt.Sprintf("typed-%d", index), ChunkSource: domain.ChunkSource{ParentID: "section"}, Content: "ROLLBACK-42 recovery procedure", TokenCount: 8, Metadata: labels})
				embeddings = append(embeddings, domain.DocumentChunkEmbedding{Provider: "test", Model: "metadata-parity", Embedding: metadataParityVector()})
			}
			if _, err := backend.CreateDocument(domain.Document{ID: "typed-document", WorkspaceID: w.ID, Title: "ROLLBACK-42", Content: "ROLLBACK-42", Metadata: metadata, MetadataSchemaVersion: "knowledge-metadata-v1"}, chunks, embeddings); err != nil {
				t.Fatal(err)
			}
			for _, tc := range []struct {
				field, op, value string
				ids              []string
			}{
				{"document.metadata.product", "eq", `"AgentFlow"`, []string{"typed-0", "typed-1", "typed-2"}},
				{"chunk.metadata.product", "eq", `"AgentFlow"`, []string{"typed-1"}},
				{"document.metadata.product", "in", `["absent","AgentFlow"]`, []string{"typed-0", "typed-1", "typed-2"}},
				{"document.metadata.product", "eq", `"agentflow"`, nil},
				{"document.metadata.product", "eq", `" AgentFlow"`, nil},
				{"document.metadata.language", "eq", `"en"`, nil},
				{"document.metadata.tags", "contains", `"safe"`, []string{"typed-0", "typed-1", "typed-2"}},
				{"chunk.metadata.tags", "contains", `"safe"`, nil},
				{"document.metadata.tags", "in", `["absent","safe"]`, []string{"typed-0", "typed-1", "typed-2"}},
				{"document.metadata.is_reference", "eq", `false`, []string{"typed-0", "typed-1", "typed-2"}},
				{"document.metadata.is_reference", "in", `[true]`, nil},
				{"document.metadata.priority", "eq", `9`, []string{"typed-0", "typed-1", "typed-2"}},
				{"document.metadata.priority", "in", `[1,9.00]`, []string{"typed-0", "typed-1", "typed-2"}},
				{"chunk.metadata.priority", "gt", `8.99999999999999999`, []string{"typed-1"}},
				{"chunk.metadata.priority", "gte", `9`, []string{"typed-1"}},
				{"chunk.metadata.priority", "lt", `9`, []string{"typed-0", "typed-2"}},
				{"chunk.metadata.priority", "lte", `0`, []string{"typed-0", "typed-2"}},
				{"document.metadata.published_at", "eq", `"2026-10-18T12:00:00.123456+08:00"`, []string{"typed-0", "typed-1", "typed-2"}},
				{"document.metadata.published_at", "in", `["2026-10-18T04:00:00Z","2026-10-18T04:00:00.123456Z"]`, []string{"typed-0", "typed-1", "typed-2"}},
				{"document.metadata.published_at", "gt", `"2026-10-18T04:00:00Z"`, []string{"typed-0", "typed-1", "typed-2"}},
				{"document.metadata.published_at", "lt", `"2026-10-18T04:00:00.123456Z"`, nil},
				{"document.metadata.published_at", "lte", `"2026-10-18T04:00:00.123456Z"`, []string{"typed-0", "typed-1", "typed-2"}},
			} {
				where := fmt.Sprintf(`{"field":%q,"op":%q,"value":%s}`, tc.field, tc.op, tc.value)
				search := decodeTypedSearch(t, where)
				search.WorkspaceID = w.ID
				for path, recall := range map[string]func(domain.DocumentSearch) ([]domain.RetrievedDocumentChunk, error){"semantic": backend.SearchDocumentChunks, "lexical": backend.SearchDocumentChunksLexical} {
					items, err := recall(search)
					if err != nil {
						t.Fatal(err)
					}
					if !slices.Equal(metadataChunkIDs(items), tc.ids) {
						t.Fatalf("%s %s got %v want %v", path, where, metadataChunkIDs(items), tc.ids)
					}
				}
				scope := domain.DocumentContextSearch{DocumentID: "typed-document", WorkspaceID: w.ID, ParentID: "section", ChunkIndex: 1, MetadataFilter: search.MetadataFilter}
				for _, path := range []string{"parent", "adjacent"} {
					if path == "adjacent" {
						scope.ParentID = ""
						scope.NeighborWindow = 1
					}
					items, err := backend.ListDocumentContextChunks(scope)
					if err != nil {
						t.Fatal(err)
					}
					if !slices.Equal(metadataChunkIDs(items), tc.ids) {
						t.Fatalf("%s %s got %v want %v", path, where, metadataChunkIDs(items), tc.ids)
					}
				}
				t.Logf("typed-filter-parity backend=%s where=%s eligible=%v", name, where, tc.ids)
			}
			search := decodeTypedSearch(t, `{"field":"chunk.metadata.priority","op":"gte","value":9}`)
			search.WorkspaceID = w.ID
			items, err := backend.SearchDocumentChunks(search)
			if err != nil {
				t.Fatal(err)
			}
			// A bad expansion adapter returns all same-document chunks. Selector
			// must independently reject the parent and neighbor's lower priority.
			selected, _, _, err := rag.NewContextSelector(unfilteredTypedStore{backend, false}).Select(search, items)
			if err != nil || !slices.Equal(metadataChunkIDs(selected), []string{"typed-1"}) {
				t.Fatalf("expansion escape: %v %v", metadataChunkIDs(selected), err)
			}
			// A bad dense or lexical adapter is a protocol error, not silent
			// post-limit filtering that would hide missing eligible candidates.
			for _, lexical := range []bool{false, true} {
				_, err := rag.NewRetrievalPipeline(unfilteredTypedStore{backend, lexical}).Search(t.Context(), search, 5, rag.Embedding{Vector: metadataParityVector(), Provider: "test", Model: "metadata-parity", Dimensions: 1536})
				if !errors.Is(err, eligibility.ErrInvariant) {
					t.Fatalf("recall escape not detected: %v", err)
				}
			}
			// Invalid direct consumers must fail even with no data/no parent.
			for _, where := range []string{
				`{"field":"document.metadata.workspace_id","op":"eq","value":"x"}`,
				strings.Repeat(`{"all":[`, 5) + `{"field":"document.metadata.product","op":"eq","value":"x"}` + strings.Repeat(`]}`, 5),
				`{"all":[` + strings.Repeat(`{"field":"document.metadata.product","op":"eq","value":"x"},`, 32) + `{"field":"document.metadata.product","op":"eq","value":"x"}]}`,
				`{"field":"document.metadata.product","op":"in","value":"not-an-array"}`,
				`{"field":"document.metadata.product","op":"contains","value":"x"}`,
			} {
				bad := decodeTypedSearch(t, where)
				for _, recall := range []func(domain.DocumentSearch) ([]domain.RetrievedDocumentChunk, error){backend.SearchDocumentChunks, backend.SearchDocumentChunksLexical} {
					if _, err := recall(bad); !errors.Is(err, eligibility.ErrInvalid) {
						t.Fatalf("invalid direct filter: %v", err)
					}
				}
				if _, err := backend.ListDocumentContextChunks(domain.DocumentContextSearch{MetadataFilter: bad.MetadataFilter}); !errors.Is(err, eligibility.ErrInvalid) {
					t.Fatalf("invalid context filter: %v", err)
				}
				if _, _, _, err := rag.NewContextSelector(backend).Select(bad, nil); !errors.Is(err, eligibility.ErrInvalid) {
					t.Fatalf("invalid empty selector: %v", err)
				}
			}
		})
	}
}

func TestTypedMetadataFilterBeforeRecallLimit(t *testing.T) {
	pg, err := store.NewPostgresStore(pgfixture.DatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer pg.Close()
	w, err := pg.CreateWorkspace(t.Context(), identity.SuperUserID, "Selective typed recall", "")
	if err != nil {
		t.Fatal(err)
	}
	for name, backend := range map[string]metadataParityStore{"postgres": pg, "fixture": fixturestore.New()} {
		t.Run(name, func(t *testing.T) {
			for _, eligible := range []bool{false, true} {
				id, product, count := "decoy", "Other", 41
				vector := metadataParityVector()
				created := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
				if eligible {
					id, product, count = "eligible", "AgentFlow", 1
					vector[0], vector[1] = 0.6, 0.8
					created = created.AddDate(-10, 0, 0)
				}
				chunks := make([]domain.DocumentChunk, count)
				embeddings := make([]domain.DocumentChunkEmbedding, count)
				for i := range chunks {
					chunks[i] = domain.DocumentChunk{ID: fmt.Sprintf("%s-%d", id, i), Content: "ROLLBACK-42 recovery", CreatedAt: created, Metadata: map[string]any{"product": product}}
					embeddings[i] = domain.DocumentChunkEmbedding{Provider: "test", Model: "metadata-parity", Embedding: vector}
				}
				if _, err := backend.CreateDocument(domain.Document{ID: id, WorkspaceID: w.ID, Title: "ROLLBACK-42", Content: "ROLLBACK-42", MetadataSchemaVersion: "knowledge-metadata-v1", Metadata: map[string]any{"product": product}}, chunks, embeddings); err != nil {
					t.Fatal(err)
				}
			}
			search := decodeTypedSearch(t, `{"field":"document.metadata.product","op":"eq","value":"AgentFlow"}`)
			search.WorkspaceID = w.ID
			search.Limit = 1
			for path, recall := range map[string]func(domain.DocumentSearch) ([]domain.RetrievedDocumentChunk, error){"semantic": backend.SearchDocumentChunks, "lexical": backend.SearchDocumentChunksLexical} {
				unfiltered := search
				unfiltered.MetadataFilter = nil
				before, err := recall(unfiltered)
				if err != nil || len(before) != 1 || before[0].Document.ID != "decoy" {
					t.Fatalf("fixture does not prove pre-limit: %v %v", metadataChunkIDs(before), err)
				}
				start := time.Now()
				items, err := recall(search)
				elapsed := time.Since(start)
				if err != nil || len(items) != 1 || items[0].Document.ID != "eligible" {
					t.Fatalf("%s filtered after limit: %v %v", path, metadataChunkIDs(items), err)
				}
				t.Logf("typed-limit-evidence backend=%s path=%s corpus=42 eligible=1 limit=1 returned=%v elapsed_ns=%d limitations=single-sample-not-a-benchmark", name, path, metadataChunkIDs(items), elapsed.Nanoseconds())
			}
		})
	}
}

func decodeTypedSearch(t *testing.T, where string) domain.DocumentSearch {
	t.Helper()
	var search domain.DocumentSearch
	decoder := json.NewDecoder(strings.NewReader(`{"query":"ROLLBACK-42","metadata_filter":{"schema_version":"knowledge-metadata-v1","where":` + where + `}}`))
	decoder.UseNumber()
	if err := decoder.Decode(&search); err != nil {
		t.Fatal(err)
	}
	search.Embedding = metadataParityVector()
	search.LexicalTerms = []string{"rollback", "42"}
	search.Limit = 20
	return search
}

type unfilteredTypedStore struct {
	metadataParityStore
	lexical bool
}

func (s unfilteredTypedStore) SearchDocumentChunks(search domain.DocumentSearch) ([]domain.RetrievedDocumentChunk, error) {
	if !s.lexical {
		search.MetadataFilter = nil
	}
	return s.metadataParityStore.SearchDocumentChunks(search)
}
func (s unfilteredTypedStore) SearchDocumentChunksLexical(search domain.DocumentSearch) ([]domain.RetrievedDocumentChunk, error) {
	if s.lexical {
		search.MetadataFilter = nil
	}
	return s.metadataParityStore.SearchDocumentChunksLexical(search)
}
func (s unfilteredTypedStore) ListDocumentContextChunks(search domain.DocumentContextSearch) ([]domain.RetrievedDocumentChunk, error) {
	search.MetadataFilter = nil
	return s.metadataParityStore.ListDocumentContextChunks(search)
}
