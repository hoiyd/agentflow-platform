package store_test

import (
	"encoding/json"
	"fmt"
	"slices"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/identity"
	"agentflow-platform/apps/api/internal/rag"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/testsupport/fixturestore"
	"agentflow-platform/apps/api/internal/testsupport/pgfixture"
)

// Failure inventory: Chunk-overrides-Document instead of OR, OR between keys
// instead of AND, trimming/case folding, null becoming text, scalar formatting,
// filtering after LIMIT, scope escape, and selector rechecks rejecting SQL hits.
func TestDocumentMetadataFilterStoreParity(t *testing.T) {
	pg, err := store.NewPostgresStore(pgfixture.DatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pg.Close() })
	fixture := fixturestore.New()
	foreign, err := pg.CreateWorkspace(t.Context(), identity.SuperUserID, "Foreign metadata corpus", "")
	if err != nil {
		t.Fatal(err)
	}
	for index, tc := range metadataFilterCases() {
		t.Run(tc.name, func(t *testing.T) {
			workspace, err := pg.CreateWorkspace(t.Context(), identity.SuperUserID, tc.name, "")
			if err != nil {
				t.Fatal(err)
			}
			docID := fmt.Sprintf("metadata-parity-%d", index)
			evidence := map[string]map[string][]string{}
			for name, backend := range map[string]metadataParityStore{"postgres": pg, "fixture": fixture} {
				seedMetadataDocument(t, backend, docID, workspace.ID, tc.document, tc.chunk)
				seedMetadataDocument(t, backend, docID+"-foreign", foreign.ID, tc.document, tc.chunk)
				// Another Document in the same Workspace must not enter expansion.
				seedMetadataDocument(t, backend, docID+"-other", workspace.ID, tc.document, tc.chunk)
				search := domain.DocumentSearch{Query: "metadata parity", WorkspaceID: workspace.ID,
					Embedding: metadataParityVector(), Metadata: tc.filter, Limit: 20}
				semantic, err := backend.SearchDocumentChunks(search)
				if err != nil {
					t.Fatal(err)
				}
				lexical, err := backend.SearchDocumentChunksLexical(search)
				if err != nil {
					t.Fatal(err)
				}
				scope := domain.DocumentContextSearch{DocumentID: docID, WorkspaceID: workspace.ID, Metadata: tc.filter,
					ParentID: "section", ChunkIndex: 1}
				parent, err := backend.ListDocumentContextChunks(scope)
				if err != nil {
					t.Fatal(err)
				}
				scope.ParentID, scope.NeighborWindow = "", 1
				adjacent, err := backend.ListDocumentContextChunks(scope)
				if err != nil {
					t.Fatal(err)
				}
				results := map[string][]string{"semantic": metadataChunkIDs(semantic), "lexical": metadataChunkIDs(lexical),
					"parent": metadataChunkIDs(parent), "adjacent": metadataChunkIDs(adjacent)}
				if tc.match {
					// Exercise the real expansion recheck and transformation, not just Store predicates.
					var seed domain.RetrievedDocumentChunk
					for _, item := range semantic {
						if item.Chunk.ID == docID+"-1" {
							seed = item
						}
					}
					selected, _, _, err := rag.NewContextSelector(backend).Select(search, []domain.RetrievedDocumentChunk{seed})
					if err != nil {
						t.Fatal(err)
					}
					results["selected_parent"] = metadataChunkIDs(selected)
				}
				evidence[name] = results
				for path, got := range results {
					want := []string{}
					if tc.match {
						want = []string{docID + "-0", docID + "-1"}
						if path == "adjacent" || path == "semantic" || path == "lexical" {
							want = append(want, docID+"-2")
						}
						if path == "semantic" || path == "lexical" {
							want = append(want, docID+"-other-0", docID+"-other-1", docID+"-other-2")
						}
					}
					slices.Sort(want)
					if !slices.Equal(got, want) {
						t.Errorf("%s/%s eligibility: got %v want %v", name, path, got, want)
					}
				}
			}
			encoded, err := json.Marshal(map[string]any{"case": tc.name, "document_metadata": tc.document, "chunk_metadata": tc.chunk,
				"filter": tc.filter, "expected_match": tc.match, "workspace_id": workspace.ID, "document_id": docID,
				"embedding": "test/metadata-parity; 1536 dimensions, unit vector on axis 0", "query": "metadata parity",
				"results": evidence, "limitations": "deterministic Store/selector eligibility; no ranking or live-model quality claim"})
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("metadata-parity-evidence %s", encoded)
		})
	}
}

func TestDocumentMetadataFilterAppliesBeforeRecallLimit(t *testing.T) {
	pg, err := store.NewPostgresStore(pgfixture.DatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pg.Close() })
	workspace, err := pg.CreateWorkspace(t.Context(), identity.SuperUserID, "Bounded filtered recall", "")
	if err != nil {
		t.Fatal(err)
	}
	for name, backend := range map[string]metadataParityStore{"postgres": pg, "fixture": fixturestore.New()} {
		t.Run(name, func(t *testing.T) {
			// Ineligible candidates score higher in BOTH recall paths. Fixed source
			// timestamps avoid depending on execution speed for the recency tie-break.
			for _, eligible := range []bool{true, false} {
				id, env, content, count := "distractors", "dev", "metadata parity", 21
				vector := metadataParityVector()
				created := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
				if eligible {
					id, env, content, count = "eligible", "prod", "metadata release parity", 1
					vector[0], vector[1] = 0.6, 0.8
					created = created.AddDate(-10, 0, 0)
				}
				chunks := make([]domain.DocumentChunk, count)
				embeddings := make([]domain.DocumentChunkEmbedding, count)
				for index := range chunks {
					chunks[index] = domain.DocumentChunk{ID: fmt.Sprintf("%s-%d", id, index), Content: content, CreatedAt: created}
					embeddings[index] = domain.DocumentChunkEmbedding{Provider: "test", Model: "metadata-parity", Embedding: vector}
				}
				if _, err := backend.CreateDocument(domain.Document{ID: id, WorkspaceID: workspace.ID, Title: id, Content: content,
					Metadata: map[string]any{"env": env}}, chunks, embeddings); err != nil {
					t.Fatal(err)
				}
			}
			search := domain.DocumentSearch{Query: "metadata parity", LexicalTerms: []string{"metadata", "parity"},
				WorkspaceID: workspace.ID, Embedding: metadataParityVector(), Metadata: map[string]string{"env": "prod"}, Limit: 1}
			for path, recall := range map[string]func(domain.DocumentSearch) ([]domain.RetrievedDocumentChunk, error){
				"semantic": backend.SearchDocumentChunks, "lexical": backend.SearchDocumentChunksLexical,
			} {
				items, err := recall(search)
				if err != nil || len(items) != 1 || items[0].Document.ID != "eligible" {
					t.Fatalf("%s filtered after LIMIT: IDs=%v err=%v", path, metadataChunkIDs(items), err)
				}
				t.Logf("metadata-limit-evidence backend=%s path=%s workspace=%s filter=env:prod limit=1 distractors=21 eligible=%v", name, path, workspace.ID, metadataChunkIDs(items))
			}
		})
	}
}

type metadataParityStore interface {
	rag.SearchStore
	CreateDocument(domain.Document, []domain.DocumentChunk, []domain.DocumentChunkEmbedding) (domain.Document, error)
}

func seedMetadataDocument(t *testing.T, backend metadataParityStore, id, workspace string, document, chunk map[string]any) {
	t.Helper()
	chunks := make([]domain.DocumentChunk, 3)
	embeddings := make([]domain.DocumentChunkEmbedding, 3)
	for index := range chunks {
		chunks[index] = domain.DocumentChunk{ID: fmt.Sprintf("%s-%d", id, index), ChunkSource: domain.ChunkSource{ParentID: "section"},
			Content: fmt.Sprintf("metadata parity source %d", index), TokenCount: 8, Metadata: chunk}
		embeddings[index] = domain.DocumentChunkEmbedding{Provider: "test", Model: "metadata-parity", Embedding: metadataParityVector()}
	}
	chunks[2].ParentID = "another-section"
	if _, err := backend.CreateDocument(domain.Document{ID: id, WorkspaceID: workspace, Title: "Metadata parity", Content: "metadata parity",
		Metadata: document}, chunks, embeddings); err != nil {
		t.Fatal(err)
	}
}

func metadataParityVector() []float64 {
	vector := make([]float64, 1536)
	vector[0] = 1
	return vector
}

func metadataChunkIDs(items []domain.RetrievedDocumentChunk) []string {
	ids := []string{}
	for _, item := range items {
		if len(item.SourceChunkIDs) > 0 {
			ids = append(ids, item.SourceChunkIDs...)
		} else {
			ids = append(ids, item.Chunk.ID)
		}
	}
	slices.Sort(ids)
	return slices.Compact(ids)
}

type metadataFilterCase struct {
	name     string
	document map[string]any
	chunk    map[string]any
	filter   map[string]string
	match    bool
}

func metadataFilterCases() []metadataFilterCase {
	return []metadataFilterCase{
		{"no_filter", nil, nil, nil, true},
		{"document_only", map[string]any{"env": "prod"}, nil, map[string]string{"env": "prod"}, true},
		{"chunk_only", nil, map[string]any{"env": "prod"}, map[string]string{"env": "prod"}, true},
		{"conflict_document_matches", map[string]any{"env": "prod"}, map[string]any{"env": "dev"}, map[string]string{"env": "prod"}, true},
		{"conflict_chunk_matches", map[string]any{"env": "prod"}, map[string]any{"env": "dev"}, map[string]string{"env": "dev"}, true},
		{"conflict_neither_matches", map[string]any{"env": "prod"}, map[string]any{"env": "dev"}, map[string]string{"env": "test"}, false},
		{"keys_and_layers_or", map[string]any{"product": "AgentFlow", "env": "dev"}, map[string]any{"product": "other", "env": "prod"}, map[string]string{"product": "AgentFlow", "env": "prod"}, true},
		{"one_key_fails", map[string]any{"product": "AgentFlow", "env": "dev"}, nil, map[string]string{"product": "AgentFlow", "env": "prod"}, false},
		{"stored_whitespace_is_significant", map[string]any{"env": " prod "}, nil, map[string]string{"env": "prod"}, false},
		{"filter_whitespace_is_significant", map[string]any{"env": "prod"}, nil, map[string]string{"env": " prod "}, false},
		{"exact_whitespace_matches", map[string]any{"env": " prod "}, nil, map[string]string{"env": " prod "}, true},
		{"case_is_significant", map[string]any{"env": "Prod"}, nil, map[string]string{"env": "prod"}, false},
		{"key_case_is_significant", map[string]any{"Env": "prod"}, nil, map[string]string{"env": "prod"}, false},
		{"missing_is_not_empty", nil, nil, map[string]string{"env": ""}, false},
		{"null_is_not_empty", map[string]any{"env": nil}, nil, map[string]string{"env": ""}, false},
		{"null_is_not_text", map[string]any{"env": nil}, nil, map[string]string{"env": "null"}, false},
		{"empty_string_matches", map[string]any{"env": ""}, nil, map[string]string{"env": ""}, true},
		{"null_chunk_does_not_shadow_document", map[string]any{"env": "prod"}, map[string]any{"env": nil}, map[string]string{"env": "prod"}, true},
		{"number_as_text", map[string]any{"version": 1}, nil, map[string]string{"version": "1"}, true},
		{"string_as_text", map[string]any{"version": "1"}, nil, map[string]string{"version": "1"}, true},
		{"numeric_filter_is_not_numeric_comparison", map[string]any{"version": 1}, nil, map[string]string{"version": "1.0"}, false},
		{"boolean_as_text", nil, map[string]any{"active": true}, map[string]string{"active": "true"}, true},
		{"boolean_case_is_significant", nil, map[string]any{"active": true}, map[string]string{"active": "True"}, false},
		{"false_as_text", map[string]any{"active": false}, nil, map[string]string{"active": "false"}, true},
		{"decimal_scale_survives_read", map[string]any{"version": json.Number("1.00")}, nil, map[string]string{"version": "1.00"}, true},
		{"large_integer_survives_read", map[string]any{"version": json.Number("9007199254740993")}, nil, map[string]string{"version": "9007199254740993"}, true},
		{"exponent_is_decimal_text", map[string]any{"version": 1e21}, nil, map[string]string{"version": "1000000000000000000000"}, true},
		{"small_exponent_is_decimal_text", map[string]any{"version": 1e-7}, nil, map[string]string{"version": "0.0000001"}, true},
		{"array_is_json_text_not_membership", map[string]any{"tags": []any{"prod", true, nil, 1}}, nil, map[string]string{"tags": `["prod", true, null, 1]`}, true},
		{"array_member_does_not_match", map[string]any{"tags": []string{"prod"}}, nil, map[string]string{"tags": "prod"}, false},
		{"object_is_jsonb_text", map[string]any{"scope": map[string]any{"zz": 2, "b": 1}}, nil, map[string]string{"scope": `{"b": 1, "zz": 2}`}, true},
		{"object_spacing_is_significant", map[string]any{"scope": map[string]any{"b": 1}}, nil, map[string]string{"scope": `{"b":1}`}, false},
		{"structured_unicode_text", map[string]any{"tags": []string{"<prod>", "行\u2028间", "quote\"", "new\nline"}}, nil, map[string]string{"tags": "[\"<prod>\", \"行\u2028间\", \"quote\\\"\", \"new\\nline\"]"}, true},
		{"literal_key_not_sql", map[string]any{"x' OR 1=1 --": "prod"}, nil, map[string]string{"x' OR 1=1 --": "prod"}, true},
	}
}
