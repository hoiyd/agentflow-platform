package httpapi

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/identity"
	"agentflow-platform/apps/api/internal/knowledge"
	"agentflow-platform/apps/api/internal/rag/metadataschema"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/testsupport/fixturestore"
	"agentflow-platform/apps/api/internal/testsupport/pgfixture"
)

// Real HTTP/Knowledge/Store behavior; the external embedder alone is controlled.
// Failure inventory: docs/knowledge/metadata-schema.md.
func TestMetadataSchemaContract(t *testing.T) {
	t.Run("fixture", func(t *testing.T) { runMetadataSchemaContract(t, fixturestore.New(), "1", "") })
	t.Run("postgres", func(t *testing.T) {
		url := pgfixture.DatabaseURL(t)
		s, err := store.NewPostgresStore(url)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		workspace, err := s.CreateWorkspace(t.Context(), identity.SuperUserID, "Schema contract", "")
		if err != nil {
			t.Fatal(err)
		}
		runMetadataSchemaContract(t, s, workspace.ID, url)
	})
}

func runMetadataSchemaContract(t *testing.T, backend store.Store, workspace, databaseURL string) {
	t.Helper()
	embedder := &metadataEmbedder{}
	h := &Handler{store: backend, knowledge: knowledge.NewKnowledgeBase(backend, embedder)}
	mux := http.NewServeMux()
	h.registerDocumentRoutes(mux)
	server := httptest.NewServer(mux)
	defer server.Close()
	call := func(method, path string, input any, want int) map[string]any {
		t.Helper()
		var body []byte
		if raw, ok := input.(string); ok {
			body = []byte(raw)
		} else {
			body, _ = json.Marshal(input)
		}
		req, _ := http.NewRequest(method, server.URL+path, bytes.NewReader(body))
		req.Header.Set("X-Workspace-ID", workspace)
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		data, _ := io.ReadAll(response.Body)
		if response.StatusCode != want {
			t.Fatalf("%s %s: want %d got %d %s", method, path, want, response.StatusCode, data)
		}
		var result map[string]any
		if err := json.Unmarshal(data, &result); err != nil {
			t.Fatalf("response JSON: %v %s", err, data)
		}
		return result
	}
	const version = "knowledge-metadata-v1"
	schema := call("GET", "/api/documents/metadata-schema", nil, 200)
	if schema["version"] != version {
		t.Fatalf("schema: %v", schema)
	}
	input := map[string]any{"source_key": "schema-release", "title": "Release", "content": "Release AgentFlow production safely.", "metadata_schema_version": version,
		"metadata": map[string]any{"product": "AgentFlow", "environment": "production", "language": "en", "tags": []string{"release"}, "priority": 1.5, "is_reference": false, "published_at": "2026-10-18T12:00:00+08:00", "extra": map[string]any{"keep": true}}}
	created := call("POST", "/api/documents", input, 201)
	path := "/api/documents/" + created["id"].(string)
	if created["metadata_schema_version"] != version || created["metadata"].(map[string]any)["published_at"] != "2026-10-18T04:00:00Z" {
		t.Fatalf("created: %v", created)
	}
	before := call("GET", path, nil, 200)
	calls := embedder.calls.Load()
	for name, metadata := range map[string]map[string]any{
		"wrong_string": {"product": 1}, "null": {"product": nil}, "enum": {"environment": "other"},
		"array_type": {"tags": "release"}, "array_item": {"tags": []any{1}}, "array_length": {"tags": make([]string, 33)},
		"item_length": {"tags": []string{strings.Repeat("x", 65)}}, "empty_string": {"product": ""},
		"string_length": {"product": strings.Repeat("x", 121)}, "bool": {"is_reference": "true"},
		"number": {"priority": "2"}, "range": {"priority": -1}, "range_high": {"priority": 101},
		"exact_upper_bound": {"priority": json.Number("100.00000000000000001")},
		"exact_lower_bound": {"priority": json.Number("-1e-9999")},
		"date":              {"published_at": "2026-10-18"}, "date_precision": {"published_at": "2026-10-18T00:00:00.000000001Z"},
		"date_zone": {"published_at": "2026-10-18T00:00:00+24:00"},
		"empty_key": {"": "no"}, "long_key": {strings.Repeat("x", 65): "no"},
		"identity": {"workspace_id": "foreign"}, "structure": {"heading_path": "forged"},
	} {
		t.Logf("reject %s", name)
		call("PATCH", path+"/metadata", map[string]any{"expected_updated_at": created["updated_at"], "metadata": metadata}, 400)
	}
	for _, raw := range []string{
		`{"title":"one","title":"two","content":"release"}`,
		`{"title":"one","Title":"two","content":"release"}`,
		`{"title":"release","content":"release","metadata":{"extra":{"key":1,"key":2}}}`,
		`{"title":"release","content":"release"} {}`,
		`{"title":"release","content":"release","metadata":{"extra":` + strings.Repeat(`{"nested":`, 34) + `null` + strings.Repeat(`}`, 34) + `}}`,
	} {
		call("POST", "/api/documents", raw, 400)
	}
	call("PATCH", path+"/metadata", `{"expected_updated_at":"`+created["updated_at"].(string)+`","metadata":{"product":"one","product":"two"}}`, 400)
	call("PATCH", path+"/metadata", map[string]any{"expected_updated_at": created["updated_at"], "metadata": map[string]any{"extra": strings.Repeat("x", 70000)}}, 400)
	call("PATCH", path+"/metadata", map[string]any{"expected_updated_at": created["updated_at"], "metadata_schema_version": "future-v2", "metadata": map[string]any{}}, 400)
	input["metadata"] = map[string]any{"priority": "bad"}
	delete(input, "metadata_schema_version")
	call("POST", "/api/documents", input, 400) // omitted version cannot downgrade a source
	after := call("GET", path, nil, 200)
	b, _ := json.Marshal(before)
	a, _ := json.Marshal(after)
	if !bytes.Equal(b, a) || embedder.calls.Load() != calls {
		t.Fatal("validation changed state or called embeddings")
	}
	// Multipart metadata is an object, not a Go request struct: extension keys
	// differing only in case remain distinct, but exact duplicates are rejected.
	for index, tc := range []struct {
		metadata string
		want     int
	}{
		{`{"product":"AgentFlow","Label":1,"label":2}`, 201},
		{`{"product":"one","product":"two"}`, 400},
		{`{"filename":"forged.md"}`, 400},
		{`{"format":"text"}`, 400},
	} {
		var body bytes.Buffer
		form := multipart.NewWriter(&body)
		part, err := form.CreateFormFile("file", fmt.Sprintf("schema-%d.md", index))
		if err != nil {
			t.Fatal(err)
		}
		_, _ = part.Write([]byte("# Release\n\nAgentFlow release."))
		_ = form.WriteField("metadata_schema_version", version)
		_ = form.WriteField("metadata", tc.metadata)
		_ = form.Close()
		req, _ := http.NewRequest("POST", server.URL+"/api/documents/upload", &body)
		req.Header.Set("Content-Type", form.FormDataContentType())
		req.Header.Set("X-Workspace-ID", workspace)
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != tc.want {
			t.Fatalf("multipart %d: %d %s", index, response.StatusCode, data)
		}
	}
	patch := map[string]any{"expected_updated_at": created["updated_at"], "metadata": map[string]any{"product": "AgentFlow", "priority": 3}}
	updated := call("PATCH", path+"/metadata", patch, 200)
	if updated["metadata_schema_version"] != version {
		t.Fatal("PATCH dropped schema")
	}
	patch["metadata_schema_version"] = version
	retry := call("PATCH", path+"/metadata", patch, 200)
	if retry["updated_at"] != updated["updated_at"] {
		t.Fatal("schema-aware retry was not idempotent")
	}
	current, currentChunks, _, err := backend.GetDocumentInWorkspace(workspace, created["id"].(string))
	if err != nil {
		t.Fatal(err)
	}
	vector := make([]float64, 1536)
	vector[0] = 1
	search := domain.DocumentSearch{WorkspaceID: workspace, Query: "AgentFlow release", LexicalTerms: []string{"agentflow", "release"}, Embedding: vector, Limit: 20}
	for _, lookup := range []func(domain.DocumentSearch) ([]domain.RetrievedDocumentChunk, error){backend.SearchDocumentChunks, backend.SearchDocumentChunksLexical} {
		hits, err := lookup(search)
		if err != nil || len(hits) == 0 {
			t.Fatalf("schema recall: %v %v", hits, err)
		}
		for _, hit := range hits {
			if hit.Document.ID == current.ID && hit.Document.MetadataSchemaVersion != version {
				t.Fatal("recall lost schema")
			}
		}
	}
	expanded, err := backend.ListDocumentContextChunks(domain.DocumentContextSearch{WorkspaceID: workspace, DocumentID: current.ID, ParentID: currentChunks[0].ParentID})
	if err != nil || len(expanded) == 0 || expanded[0].Document.MetadataSchemaVersion != version {
		t.Fatalf("expansion lost schema: %#v %v", expanded, err)
	}
	// The real Store also rejects internal bypasses and racing downgrades.
	for _, kind := range []string{"downgrade", "reserved", "chunk_type"} {
		direct := current
		direct.Content = "Release AgentFlow production safely."
		chunks := append([]domain.DocumentChunk(nil), currentChunks...)
		embeddings := make([]domain.DocumentChunkEmbedding, len(chunks))
		for i := range embeddings {
			embeddings[i] = domain.DocumentChunkEmbedding{Embedding: vector, Provider: current.IndexIdentity.EmbeddingProvider, Model: current.IndexIdentity.EmbeddingModel}
		}
		want := metadataschema.ErrInvalid
		switch kind {
		case "downgrade":
			direct.MetadataSchemaVersion = ""
			direct.Version = "reindexed"
			want = store.ErrDocumentMetadataConflict
		case "reserved":
			direct.Metadata = map[string]any{"workspace_id": "not-the-owner"}
		case "chunk_type":
			chunks[0].Metadata = map[string]any{"priority": "invalid"}
		}
		if _, err := backend.CreateDocument(direct, chunks, embeddings); !errors.Is(err, want) {
			t.Fatalf("direct %s: %v", kind, err)
		}
	}
	// Legacy values remain readable, including wrong known-field types and nulls.
	legacy := call("POST", "/api/documents", map[string]any{"title": "Legacy", "content": "Legacy release", "source_key": "legacy", "metadata": map[string]any{"product": 42, "unknown": nil}}, 201)
	legacyPath := "/api/documents/" + legacy["id"].(string)
	if legacy["metadata_schema_version"] != nil {
		t.Fatal("legacy was silently relabeled")
	}
	call("GET", legacyPath, nil, 200)
	call("PATCH", legacyPath+"/metadata", map[string]any{"expected_updated_at": legacy["updated_at"], "metadata_schema_version": version, "metadata": map[string]any{"product": 42}}, 400)
	adopted := call("PATCH", legacyPath+"/metadata", map[string]any{"expected_updated_at": legacy["updated_at"], "metadata_schema_version": version, "metadata": map[string]any{"product": "AgentFlow", "unknown": nil}}, 200)
	if adopted["metadata_schema_version"] != version || adopted["updated_at"] == legacy["updated_at"] {
		t.Fatal("adoption was not persisted/versioned")
	}
	// Upgrading must not certify a legacy caller label as system-derived.
	forged := call("POST", "/api/documents", map[string]any{"title": "Legacy reserved field", "content": "Release", "metadata": map[string]any{"workspace_id": "not-the-owner"}}, 201)
	forgedPath := "/api/documents/" + forged["id"].(string)
	call("PATCH", forgedPath+"/metadata", map[string]any{"expected_updated_at": forged["updated_at"], "metadata_schema_version": version, "metadata": map[string]any{"product": "AgentFlow"}}, 400)
	// Incompatible independent Chunk values must block adoption atomically.
	local, err := backend.CreateDocument(domain.Document{WorkspaceID: workspace, Title: "Local legacy", Content: "Local legacy", Metadata: map[string]any{"product": "AgentFlow"}}, []domain.DocumentChunk{{Content: "Local legacy", Metadata: map[string]any{"product": 17}}}, []domain.DocumentChunkEmbedding{{Embedding: vector}})
	if err != nil {
		t.Fatal(err)
	}
	localPath := "/api/documents/" + local.ID
	localBefore := call("GET", localPath, nil, 200)
	call("PATCH", localPath+"/metadata", map[string]any{"expected_updated_at": local.UpdatedAt, "metadata_schema_version": version, "metadata": map[string]any{"product": "AgentFlow"}}, 400)
	localAfter := call("GET", localPath, nil, 200)
	b, _ = json.Marshal(localBefore)
	a, _ = json.Marshal(localAfter)
	if !bytes.Equal(b, a) {
		t.Fatal("failed Chunk adoption left partial state")
	}
	validLocal, err := backend.CreateDocument(domain.Document{WorkspaceID: workspace, Title: "Local override", Content: "Local override", Metadata: map[string]any{"product": "AgentFlow"}}, []domain.DocumentChunk{{Content: "Local override", Metadata: map[string]any{"product": "Other product"}}}, []domain.DocumentChunkEmbedding{{Embedding: vector}})
	if err != nil {
		t.Fatal(err)
	}
	validLocalPath := "/api/documents/" + validLocal.ID
	adoptedLocal := call("PATCH", validLocalPath+"/metadata", map[string]any{"expected_updated_at": validLocal.UpdatedAt, "metadata_schema_version": version, "metadata": validLocal.Metadata}, 200)
	if adoptedLocal["updated_at"] == validLocal.UpdatedAt.Format(time.RFC3339Nano) {
		t.Fatal("schema-only adoption did not advance token")
	}
	_, localChunks, _, _ := backend.GetDocumentInWorkspace(workspace, validLocal.ID)
	if localChunks[0].Metadata["product"] != "Other product" {
		t.Fatal("valid independent override was overwritten")
	}
	if databaseURL != "" {
		reopened, err := store.NewPostgresStore(databaseURL)
		if err != nil {
			t.Fatal(err)
		}
		defer reopened.Close()
		persisted, _, found, err := reopened.GetDocumentInWorkspace(workspace, created["id"].(string))
		data, _ := json.Marshal(persisted)
		if err != nil || !found || !bytes.Contains(data, []byte(`"metadata_schema_version":"`+version+`"`)) {
			t.Fatalf("restart lost schema: %s %v", data, err)
		}
		// Recreate the pre-column shape using real legacy data, then restart twice.
		db, err := sql.Open("pgx", databaseURL)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		if _, err := db.Exec(`ALTER TABLE documents DROP COLUMN metadata_schema_version`); err != nil {
			t.Fatal(err)
		}
		for range 2 {
			migrated, err := store.NewPostgresStore(databaseURL)
			if err != nil {
				t.Fatal(err)
			}
			old, _, ok, err := migrated.GetDocumentInWorkspace(workspace, local.ID)
			if err != nil || !ok || old.Metadata["product"] != "AgentFlow" {
				t.Fatalf("legacy migration lost data: %#v %v", old, err)
			}
			data, _ := json.Marshal(old)
			if bytes.Contains(data, []byte(`"metadata_schema_version"`)) {
				t.Fatal("migration fabricated a schema")
			}
			migrated.Close()
		}
	}
}
