package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/apicontract"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/identity"
	"agentflow-platform/apps/api/internal/knowledge"
	"agentflow-platform/apps/api/internal/rag"
	"agentflow-platform/apps/api/internal/rag/eligibility"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/testsupport/fixturestore"
	"agentflow-platform/apps/api/internal/testsupport/pgfixture"
)

// Failure inventory: inactive/future/expired rows must not consume recall slots;
// labels cannot override lifecycle; invalid dates and stale/cross-workspace edits
// must leave chunks and embeddings unchanged. Owner inspection remains available.
func TestDocumentLifecycleHTTP(t *testing.T) {
	t.Run("fixture", func(t *testing.T) { documentLifecycleHTTP(t, fixturestore.New(), "1", "2") })
	t.Run("postgres", func(t *testing.T) {
		s, err := store.NewPostgresStore(pgfixture.DatabaseURL(t))
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		w, err := s.CreateWorkspace(t.Context(), identity.SuperUserID, "Lifecycle", "")
		if err != nil {
			t.Fatal(err)
		}
		foreign, err := s.CreateWorkspace(t.Context(), identity.SuperUserID, "Foreign lifecycle", "")
		if err != nil {
			t.Fatal(err)
		}
		documentLifecycleHTTP(t, s, w.ID, foreign.ID)
	})
}

func documentLifecycleHTTP(t *testing.T, backend store.Store, workspace, foreign string) {
	t.Helper()
	embedder := &metadataEmbedder{}
	h := &Handler{store: backend, knowledge: knowledge.NewKnowledgeBase(backend, embedder)}
	mux := http.NewServeMux()
	h.registerDocumentRoutes(mux)
	call := func(method, path, scope string, body any, status int) map[string]any {
		t.Helper()
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest(method, path, strings.NewReader(string(data)))
		r.Header.Set("X-Workspace-ID", scope)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != status {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, w.Code, status, w.Body.String())
		}
		var result map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if status < 300 && result["id"] != nil {
			var contract apicontract.KnowledgeDocument
			if err := json.Unmarshal(w.Body.Bytes(), &contract); err != nil || contract.Lifecycle == nil || contract.Lifecycle.Status == nil || !contract.Lifecycle.Status.Valid() {
				t.Fatalf("serialized document violates generated client contract: %s", w.Body.String())
			}
		}
		return result
	}
	create := func(key string, lifecycle any) map[string]any {
		body := map[string]any{"title": key, "source_key": key, "content": "ROLLBACK-42 production rollback guide.", "metadata": map[string]any{"status": "active", "valid_until": "9999-01-01T00:00:00Z"}}
		if lifecycle != nil {
			body["lifecycle"] = lifecycle
		}
		return call("POST", "/api/documents", workspace, body, 201)
	}
	active := create("active", nil)
	if active["lifecycle"] == nil || active["lifecycle"].(map[string]any)["status"] != "active" {
		t.Fatalf("missing legacy/default lifecycle: %v", active)
	}
	for _, entry := range []struct {
		name      string
		lifecycle map[string]any
	}{
		{"draft", map[string]any{"status": "draft"}},
		{"withdrawn", map[string]any{"status": "withdrawn"}},
		{"expired", map[string]any{"valid_until": "2000-01-01T00:00:00Z"}},
		{"future", map[string]any{"valid_from": "9999-01-01T00:00:00Z"}},
	} {
		create(entry.name, entry.lifecycle)
	}
	// A withdrawn old embedding space must not block search of active sources.
	old, oldChunks, err := rag.BuildDocument(domain.DocumentIngestRequest{WorkspaceID: workspace, Title: "Old embedding space", Content: "ROLLBACK-42 obsolete guide.", Lifecycle: &domain.DocumentLifecycle{Status: "withdrawn"}})
	if err != nil {
		t.Fatal(err)
	}
	vector := make([]float64, 1536)
	vector[0] = 1
	if _, err := backend.CreateDocument(old, oldChunks, []domain.DocumentChunkEmbedding{{Provider: "obsolete", Model: "obsolete", Dimensions: 1536, Embedding: vector}}); err != nil {
		t.Fatal(err)
	}
	search := func(want int) {
		t.Helper()
		response := call("POST", "/api/rag/search", workspace, map[string]any{"query": "ROLLBACK-42", "limit": 1}, 200)
		items := response["items"].([]any)
		if len(items) != want {
			t.Fatalf("eligible results: %v", response)
		}
		if want > 0 && items[0].(map[string]any)["document"].(map[string]any)["id"] != active["id"] {
			t.Fatalf("ineligible document entered context: %v", response)
		}
	}
	search(1)
	before := embedder.calls.Load()
	path := "/api/documents/" + active["id"].(string) + "/metadata"
	patch := map[string]any{"expected_updated_at": active["updated_at"], "metadata": active["metadata"], "lifecycle": map[string]any{"status": "withdrawn", "published_at": "2026-10-19T08:00:00+08:00"}}
	withdrawn := call("PATCH", path, workspace, patch, 200)
	if withdrawn["lifecycle"].(map[string]any)["published_at"] != "2026-10-19T00:00:00Z" {
		t.Fatal("publication time was not normalized")
	}
	search(0)
	before = embedder.calls.Load()
	// Retrying the same write is idempotent, but a stale different edit conflicts.
	if retry := call("PATCH", path, workspace, patch, 200); retry["updated_at"] != withdrawn["updated_at"] {
		t.Fatal("retry changed revision")
	}
	patch["lifecycle"] = map[string]any{"status": "active"}
	call("PATCH", path, workspace, patch, 409)
	call("PATCH", path, foreign, patch, 404)
	patch["expected_updated_at"] = withdrawn["updated_at"]
	for _, invalid := range []map[string]any{
		{"status": "deleted"}, {"valid_from": "2026-10-19"}, {"valid_from": "2026-10-19T00:00:00+24:00"},
		{"status": nil}, {"status": ""}, {"valid_from": nil}, {"valid_from": ""}, {"Status": "active"}, {"unknown": "active"},
		{"valid_until": "2026-10-19T00:00:00.000000001Z"}, {"published_at": "0000-01-01T00:00:00Z"},
		{"valid_from": "2026-10-19T00:00:00Z", "valid_until": "2026-10-19T00:00:00Z"},
		{"valid_from": "2026-10-20T00:00:00Z", "valid_until": "2026-10-19T00:00:00Z"},
	} {
		patch["lifecycle"] = invalid
		call("PATCH", path, workspace, patch, 400)
		call("POST", "/api/documents", workspace, map[string]any{"title": "invalid lifecycle", "content": "Synthetic text.", "lifecycle": invalid}, 400)
	}
	for _, invalid := range []any{nil, []any{}, "active"} {
		patch["lifecycle"] = invalid
		call("PATCH", path, workspace, patch, 400)
	}
	detail := call("GET", "/api/documents/"+active["id"].(string), workspace, nil, 200)
	if detail["document"].(map[string]any)["updated_at"] != withdrawn["updated_at"] {
		t.Fatal("rejected edits changed document")
	}
	patch["lifecycle"] = map[string]any{}
	restored := call("PATCH", path, workspace, patch, 200)
	if restored["lifecycle"].(map[string]any)["status"] != "active" {
		t.Fatal("empty lifecycle did not clear restrictions")
	}
	if embedder.calls.Load() != before {
		t.Fatal("lifecycle edit regenerated embeddings")
	}
	search(1)
	// The ingest shortcut must carry lifecycle through the same zero-embedding
	// transaction, while direct Store/Knowledge callers also validate inputs.
	before = embedder.calls.Load()
	restored = call("POST", "/api/documents", workspace, map[string]any{"title": "active", "source_key": "active", "content": "ROLLBACK-42 production rollback guide.", "metadata": active["metadata"], "expected_updated_at": restored["updated_at"], "lifecycle": map[string]any{"status": "draft"}}, 201)
	if restored["lifecycle"].(map[string]any)["status"] != "draft" || embedder.calls.Load() != before {
		t.Fatal("conditional lifecycle ingest bypassed metadata transaction")
	}
	if _, err := h.knowledge.Ingest(t.Context(), domain.DocumentIngestRequest{WorkspaceID: workspace, Title: "Invalid", Content: "Synthetic text.", Lifecycle: &domain.DocumentLifecycle{Status: "unknown"}}); !errors.Is(err, eligibility.ErrLifecycle) || embedder.calls.Load() != before {
		t.Fatal("direct Knowledge caller bypassed lifecycle validation")
	}
	var token time.Time
	if err := token.UnmarshalText([]byte(restored["updated_at"].(string))); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.UpdateDocumentMetadata(workspace, active["id"].(string), domain.DocumentMetadataUpdate{ExpectedUpdatedAt: token, Metadata: active["metadata"].(map[string]any), Lifecycle: &domain.DocumentLifecycle{Status: "unknown"}}); !errors.Is(err, eligibility.ErrLifecycle) {
		t.Fatal("direct Store update bypassed lifecycle validation")
	}
	if _, err := backend.CreateDocument(domain.Document{WorkspaceID: workspace, Title: "Invalid", Content: "Synthetic text.", Lifecycle: domain.DocumentLifecycle{Status: "unknown"}}, []domain.DocumentChunk{{Content: "Synthetic text."}}, []domain.DocumentChunkEmbedding{{Embedding: vector}}); !errors.Is(err, eligibility.ErrLifecycle) {
		t.Fatal("direct Store ingestion bypassed lifecycle validation")
	}
	// Reindexing must not implicitly reactivate a source withdrawn after lookup.
	h.knowledge = knowledge.NewKnowledgeBase(metadataReindexRaceStore{Store: backend, reindex: func() {
		var current struct {
			UpdatedAt time.Time      `json:"updated_at"`
			Metadata  map[string]any `json:"metadata"`
		}
		data, _ := json.Marshal(restored)
		if err := json.Unmarshal(data, &current); err != nil {
			t.Fatal(err)
		}
		if _, err := backend.UpdateDocumentMetadata(workspace, active["id"].(string), domain.DocumentMetadataUpdate{ExpectedUpdatedAt: current.UpdatedAt, Metadata: current.Metadata, Lifecycle: &domain.DocumentLifecycle{Status: "withdrawn"}}); err != nil {
			t.Fatal(err)
		}
	}}, embedder)
	call("POST", "/api/documents", workspace, map[string]any{"title": "active", "source_key": "active", "content": "ROLLBACK-42 changed content.", "metadata": active["metadata"]}, 409)
	detail = call("GET", "/api/documents/"+active["id"].(string), workspace, nil, 200)
	if detail["document"].(map[string]any)["lifecycle"].(map[string]any)["status"] != "withdrawn" {
		t.Fatal("concurrent reindex reactivated source")
	}
}
