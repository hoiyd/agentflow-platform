package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/identity"
	"agentflow-platform/apps/api/internal/knowledge"
	"agentflow-platform/apps/api/internal/rag"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/testsupport/fixturestore"
	"agentflow-platform/apps/api/internal/testsupport/pgfixture"
)

// Failure inventory: docs/knowledge/typed-metadata-filters.md. Exercise actual
// HTTP, Knowledge and Store behavior; only the remote embedding is controlled.
func TestTypedMetadataFilters(t *testing.T) {
	t.Run("fixture", func(t *testing.T) { typedMetadataHTTP(t, fixturestore.New(), "1", "2") })
	t.Run("postgres", func(t *testing.T) {
		s, err := store.NewPostgresStore(pgfixture.DatabaseURL(t))
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		w, err := s.CreateWorkspace(t.Context(), identity.SuperUserID, "Typed filters", "")
		if err != nil {
			t.Fatal(err)
		}
		foreign, err := s.CreateWorkspace(t.Context(), identity.SuperUserID, "Foreign typed filters", "")
		if err != nil {
			t.Fatal(err)
		}
		typedMetadataHTTP(t, s, w.ID, foreign.ID)
	})
}

func typedMetadataHTTP(t *testing.T, backend store.Store, workspace, foreign string) {
	t.Helper()
	embedder := &metadataEmbedder{}
	h := &Handler{store: backend, knowledge: knowledge.NewKnowledgeBase(backend, embedder)}
	mux := http.NewServeMux()
	h.registerDocumentRoutes(mux)
	call := func(raw string, want int) map[string]any {
		t.Helper()
		r := httptest.NewRequest("POST", "/api/rag/search", strings.NewReader(raw))
		r.Header.Set("X-Workspace-ID", workspace)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("want %d got %d: %s input=%s", want, w.Code, w.Body.String(), raw)
		}
		var result map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	for _, entry := range []struct{ id, scope, version, product string }{
		{"typed-eligible", workspace, "knowledge-metadata-v1", "AgentFlow"},
		{"typed-decoy", workspace, "knowledge-metadata-v1", "Other"},
		{"typed-foreign", foreign, "knowledge-metadata-v1", "AgentFlow"},
		{"typed-legacy", workspace, "", "AgentFlow"},
	} {
		_, err := h.knowledge.Ingest(t.Context(), domain.DocumentIngestRequest{WorkspaceID: entry.scope, Title: entry.id,
			Content: "ROLLBACK-42 AgentFlow production rollback guide.", MetadataSchemaVersion: entry.version,
			Metadata: map[string]any{"product": entry.product, "environment": "production", "tags": []string{"release", "safe"},
				"priority": json.Number("9.00"), "is_reference": false, "published_at": "2026-10-18T04:00:00.123456Z"}})
		if err != nil {
			t.Fatal(err)
		}
	}
	filter := func(where string) string { return `{"schema_version":"knowledge-metadata-v1","where":` + where + `}` }
	request := func(where, legacy string) string {
		return `{"query":"ROLLBACK-42","limit":5,"metadata_filter":` + filter(where) + legacy + `}`
	}
	for _, tc := range []struct {
		name, where string
		want        int
	}{
		{"document", `{"field":"document.metadata.product","op":"eq","value":"AgentFlow"}`, 1},
		{"chunk", `{"field":"chunk.metadata.product","op":"eq","value":"AgentFlow"}`, 1},
		{"and", `{"all":[{"field":"document.metadata.product","op":"in","value":["AgentFlow"]},{"field":"chunk.metadata.priority","op":"gte","value":9.0}]}`, 1},
		{"or", `{"any":[{"field":"document.metadata.product","op":"eq","value":"AgentFlow"},{"field":"document.metadata.product","op":"eq","value":"absent"}]}`, 1},
		{"tags", `{"all":[{"field":"document.metadata.product","op":"eq","value":"AgentFlow"},{"field":"chunk.metadata.tags","op":"contains","value":"safe"}]}`, 1},
		{"bool", `{"all":[{"field":"document.metadata.product","op":"eq","value":"AgentFlow"},{"field":"document.metadata.is_reference","op":"eq","value":false}]}`, 1},
		{"date", `{"all":[{"field":"document.metadata.product","op":"eq","value":"AgentFlow"},{"field":"chunk.metadata.published_at","op":"eq","value":"2026-10-18T12:00:00.123456+08:00"}]}`, 1},
		{"no_match", `{"field":"document.metadata.priority","op":"gt","value":9}`, 0},
		{"exact_boundary", `{"field":"document.metadata.priority","op":"lt","value":9.00000000000000001}`, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := call(request(tc.where, ""), 200)
			items := result["items"].([]any)
			if len(items) != tc.want {
				t.Fatalf("got %d want %d: %v", len(items), tc.want, result)
			}
			for _, item := range items {
				if item.(map[string]any)["document"].(map[string]any)["workspace_id"] != workspace {
					t.Fatal("scope escape")
				}
			}
			data, _ := json.Marshal(map[string]any{"case": tc.name, "filter": json.RawMessage(filter(tc.where)), "workspace": workspace, "response": result, "limitations": "controlled embeddings; eligibility, not live answer quality"})
			t.Logf("typed-filter-http-evidence %s", data)
		})
	}
	// Both optional contracts are ANDed; neither may silently replace the other.
	result := call(request(`{"field":"document.metadata.product","op":"eq","value":"AgentFlow"}`, `,"metadata":{"product":"Other"}`), 200)
	if len(result["items"].([]any)) != 0 {
		t.Fatal("legacy constraint dropped")
	}
	before := embedder.calls.Load()
	for _, invalid := range []string{
		`{}`, `null`, `{"schema_version":"future","where":{"field":"document.metadata.product","op":"eq","value":"AgentFlow"}}`,
		filter(`{}`), filter(`{"any":[]}`), filter(`{"all":[]}`),
		filter(`{"all":null,"field":"document.metadata.product","op":"eq","value":"AgentFlow"}`),
		filter(`{"all":[{"field":"document.metadata.product","op":"eq","value":"AgentFlow"}],"value":null}`),
		filter(`{"field":"document.metadata.product","Field":"document.metadata.language","op":"eq","value":"en"}`),
		filter(`{"field":"document.metadata.access_scope","op":"eq","value":"public"}`),
		filter(`{"field":"workspace_id","op":"eq","value":"foreign"}`),
		filter(`{"field":"document.metadata.product' OR 1=1 --","op":"eq","value":"a"}`),
		filter(`{"field":"document.metadata.product","op":"eq","value":null}`),
		filter(`{"field":"document.metadata.product","op":"eq","value":1}`),
		filter(`{"field":"document.metadata.product","op":"in","value":[]}`),
		filter(`{"field":"document.metadata.product","op":"in","value":["valid",1]}`),
		filter(`{"field":"document.metadata.product","op":"in","value":[` + strings.Repeat(`"a",`, 32) + `"a"]}`),
		filter(`{"field":"document.metadata.environment","op":"eq","value":"prod"}`),
		filter(`{"field":"document.metadata.priority","op":"eq","value":"9"}`),
		filter(`{"field":"document.metadata.priority","op":"gte","value":100.00000000000000001}`),
		filter(`{"field":"document.metadata.priority","op":"eq","value":-1e-9999}`),
		filter(`{"field":"document.metadata.is_reference","op":"eq","value":"false"}`),
		filter(`{"field":"document.metadata.published_at","op":"lt","value":"2026-02-30T00:00:00Z"}`),
		filter(`{"field":"document.metadata.published_at","op":"lt","value":"2026-10-18T00:00:00+24:00"}`),
		filter(`{"field":"document.metadata.published_at","op":"lt","value":"2026-10-18T00:00:00.000000001Z"}`),
		filter(`{"field":"document.metadata.published_at","op":"lt","value":"0000-01-01T00:00:00Z"}`),
		filter(`{"field":"document.metadata.published_at","op":"lt","value":"0001-01-01T00:00:00+01:00"}`),
		filter(`{"field":"document.metadata.published_at","op":"lt","value":"9999-12-31T23:59:59-01:00"}`),
		filter(`{"field":"document.metadata.product","op":"gt","value":"a"}`),
		filter(`{"field":"document.metadata.product","op":"ne","value":"a"}`),
		filter(`{"field":"document.metadata.tags","op":"eq","value":["a"]}`),
		filter(`{"any":[{"field":"document.metadata.product","op":"eq","value":"a"}],"field":"document.metadata.product","op":"eq","value":"a"}`),
		filter(`{"field":"document.metadata.product","op":"eq","value":"a","operator":"ignored"}`),
		filter(`{"field":"document.metadata.product","op":"eq","value":"a","value":"b"}`),
		filter(`{"all":[` + strings.Repeat(`{"field":"document.metadata.product","op":"eq","value":"a"},`, 32) + `{"field":"document.metadata.product","op":"eq","value":"a"}]}`),
		filter(strings.Repeat(`{"all":[`, 5) + `{"field":"document.metadata.product","op":"eq","value":"a"}` + strings.Repeat(`]}`, 5)),
	} {
		call(`{"query":"ROLLBACK-42","metadata_filter":`+invalid+`}`, 400)
	}
	call(`{"query":"ROLLBACK-42","metadata_filter":`+filter(`{"field":"document.metadata.product","op":"eq","value":"`+strings.Repeat("x", 70000)+`"}`)+`}`, 400)
	if embedder.calls.Load() != before {
		t.Fatal("invalid filter called embedder")
	}
	// SQL-like values are ordinary data, not executable predicates.
	result = call(request(`{"field":"document.metadata.product","op":"eq","value":"' OR 1=1 --"}`, ""), 200)
	if len(result["items"].([]any)) != 0 {
		t.Fatal("injection value matched")
	}
	// Omission remains backwards compatible and admits both legacy and typed rows.
	result = call(`{"query":"ROLLBACK-42","metadata":{"product":"AgentFlow"}}`, 200)
	if len(result["items"].([]any)) != 2 {
		t.Fatal("legacy behavior changed")
	}
	// Model an adapter regression, not a mocked successful API: the actual
	// Store runs without its typed constraint in exactly one recall path.
	for _, lexical := range []bool{false, true} {
		h.knowledge = knowledge.NewKnowledgeBaseWithRetriever(backend, embedder, rag.NewRetrievalPipeline(typedRecallViolation{backend, lexical}))
		failure := call(request(`{"field":"document.metadata.product","op":"eq","value":"AgentFlow"}`, ""), 500)
		if failure["code"] != "knowledge_eligibility_violation" || failure["items"] != nil || failure["citation_sources"] != nil {
			t.Fatalf("adapter failure returned partial or unclassified evidence: %v", failure)
		}
	}
}

type typedRecallViolation struct {
	store.Store
	lexical bool
}

func (s typedRecallViolation) SearchDocumentChunks(search domain.DocumentSearch) ([]domain.RetrievedDocumentChunk, error) {
	if !s.lexical {
		search.MetadataFilter = nil
	}
	return s.Store.SearchDocumentChunks(search)
}
func (s typedRecallViolation) SearchDocumentChunksLexical(search domain.DocumentSearch) ([]domain.RetrievedDocumentChunk, error) {
	if s.lexical {
		search.MetadataFilter = nil
	}
	return s.Store.SearchDocumentChunksLexical(search)
}
