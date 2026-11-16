package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"agentflow-platform/apps/api/internal/knowledge"
	"agentflow-platform/apps/api/internal/testsupport/fixturestore"
)

// Retired constraints, even empty/null or accompanied by a valid typed filter,
// must reject before embedding rather than silently broaden retrieval.
func TestRetiredMetadataFilterRejectsBeforeEmbedding(t *testing.T) {
	embedder := &metadataEmbedder{}
	h := &Handler{knowledge: knowledge.NewKnowledgeBase(fixturestore.New(), embedder)}
	for _, value := range []string{`{}`, `null`, `{"product":"Atlas"}`} {
		for _, tc := range []struct {
			name, body string
			eval       bool
		}{
			{"search", `{"query":"ROLLBACK-42","metadata":` + value + `}`, false},
			{"mixed", `{"query":"ROLLBACK-42","metadata":` + value + `,"metadata_filter":{"schema_version":"knowledge-metadata-v1","where":{"field":"document.metadata.product","op":"eq","value":"Atlas"}}}`, false},
			{"evaluation request", `{"metadata":` + value + `,"dataset":{"schema_version":"rag-golden-dataset-v1","id":"retired","version":"1","cases":[{"id":"one","query":"ROLLBACK-42","answerable":false}]}}`, true},
			{"evaluation case", `{"dataset":{"schema_version":"rag-golden-dataset-v1","id":"retired","version":"1","cases":[{"id":"one","query":"ROLLBACK-42","answerable":false,"metadata":` + value + `}]}}`, true},
		} {
			t.Run(tc.name+value, func(t *testing.T) {
				before := embedder.calls.Load()
				w := httptest.NewRecorder()
				r := httptest.NewRequest(http.MethodPost, "/api/rag/search", strings.NewReader(tc.body))
				if tc.eval {
					h.runRAGEvaluation(w, r)
				} else {
					h.searchDocumentChunks(w, r)
				}
				if w.Code != 400 || embedder.calls.Load() != before {
					t.Fatalf("retired filter accepted or spent embeddings: %d %s", w.Code, w.Body.String())
				}
			})
		}
	}
}
