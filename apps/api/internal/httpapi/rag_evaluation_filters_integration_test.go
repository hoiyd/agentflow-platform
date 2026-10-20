package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"agentflow-platform/apps/api/internal/apicontract"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/knowledge"
	"agentflow-platform/apps/api/internal/testsupport/fixturestore"
)

// Failure inventory: invalid late cases must not spend embeddings; null/typos/
// duplicate restrictions cannot silently disappear; constraints and revisions
// must survive real handler JSON and generated client decoding.
func TestRAGEvaluationFilterHTTPContract(t *testing.T) {
	embedder := &metadataEmbedder{}
	base := knowledge.NewKnowledgeBase(fixturestore.New(), embedder)
	for _, product := range []string{"Atlas", "Boreal"} {
		if _, err := base.Ingest(t.Context(), domain.DocumentIngestRequest{Title: product, SourceURI: product + ".md", WorkspaceID: "1", Content: "ROLLBACK-42 production release procedure.", MetadataSchemaVersion: "knowledge-metadata-v1", Metadata: map[string]any{"product": product}}); err != nil {
			t.Fatal(err)
		}
	}
	h := &Handler{knowledge: base}
	call := func(body string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest("POST", "/api/rag/evaluations/run", strings.NewReader(body))
		r.Header.Set("X-Workspace-ID", "1")
		w := httptest.NewRecorder()
		h.runRAGEvaluation(w, r)
		return w
	}
	for _, body := range []string{
		`{"cases":[{"id":"valid","query":"ROLLBACK-42"},{"id":"missing","query":"ROLLBACK-42","require_filter":true}]}`,
		`{"cases":[{"id":"x","query":"ROLLBACK-42","metadata_filter":null}]}`,
		`{"cases":[{"id":"x","query":"ROLLBACK-42","metadata":{"product":null}}]}`,
		`{"cases":[{"id":"x","query":"ROLLBACK-42","metadata":{"product":"Atlas"},"metadata":{"product":"Boreal"}}]}`,
		`{"cases":[{"id":"x","query":"ROLLBACK-42","metadata_filter":{"schema_version":"knowledge-metadata-v1","where":{"field":"document.metadata.priority","op":"gte","value":"80"}}}]}`,
		`{"workspace_id":"2","cases":[{"id":"x","query":"ROLLBACK-42"}]}`,
	} {
		before := embedder.calls.Load()
		response := call(body)
		if response.Code != http.StatusBadRequest || embedder.calls.Load() != before {
			t.Fatalf("unsafe batch: %s %d %s", body, response.Code, response.Body.String())
		}
	}
	body := `{"metadata":{"product":"Atlas"},"cases":[{"id":"atlas","query":"ROLLBACK-42","require_filter":true,"expected_sources":[{"source_uri":"Atlas.md"}],"forbidden_sources":[{"source_uri":"Boreal.md"}]}]}`
	var input apicontract.RAGEvaluationRunRequest
	if err := json.Unmarshal([]byte(body), &input); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	response := call(string(encoded))
	if response.Code != http.StatusOK {
		t.Fatalf("generated request rejected: %d %s", response.Code, response.Body.String())
	}
	var result domain.RAGEvaluationRunResponse
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Cases) != 1 || !result.Cases[0].Hit || result.Cases[0].Evidence.Metadata["product"] != "Atlas" {
		t.Fatalf("lost contract: %s", response.Body.String())
	}
	evidence, err := json.Marshal(result.Cases[0].Evidence)
	if err != nil {
		t.Fatal(err)
	}
	var contract apicontract.RAGEvaluationEvidence
	if err := json.Unmarshal(evidence, &contract); err != nil || contract.ExpectedSources != 1 || contract.MatchedSources != 1 || contract.SchemaVersion != "knowledge-metadata-v1" || len(contract.ContextSources) == 0 {
		t.Fatalf("invalid serialized evidence: %s %v", evidence, err)
	}
}
