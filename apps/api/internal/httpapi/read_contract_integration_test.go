package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
)

// Validate serialized handler responses, not parallel copies of transport DTOs.
func TestConsumedReadContracts(t *testing.T) {
	contract := loadResourceContract(t)
	s, run := createHTTPTestRun(t)
	h := (&Handler{store: s}).Routes()
	for _, c := range []struct{ path, method, body, schema string }{
		{"/api/conversations/" + run.ConversationID + "/task-state", "GET", "", "TaskState"},
		{"/api/conversations/" + run.ConversationID + "/task-state", "PATCH", `{"expected_version":0,"operations":[{"type":"upsert_task","task":{"id":"one","title":"Preserve contracts","status":"pending"}}]}`, "TaskStateRevision"},
		{"/api/runs/" + run.ID + "/projection", "GET", "", "RunProjectionSnapshot"},
		{"/api/runs/" + run.ID + "/replay", "GET", "", "RunReplay"},
		{"/api/runs/" + run.ID + "/model_requests", "GET", "", "ModelRequestDebugResponse"},
		{"/api/runs/" + run.ID + "/episode", "GET", "", "EpisodeReport"},
	} {
		t.Run(c.schema, func(t *testing.T) {
			response := httptest.NewRecorder()
			h.ServeHTTP(response, httptest.NewRequest(c.method, c.path, strings.NewReader(c.body)))
			if response.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			contract.validate(t, "/components/schemas/"+c.schema, response.Body.Bytes())
		})
	}
	for name, fixture := range map[string]any{
		"RecoverySummary":         domain.RecoverySummary{Reason: domain.RecoveryRunFailed},
		"ToolArtifact":            domain.ToolArtifact{ID: "artifact", SchemaVersion: 1},
		"ToolEffect":              domain.ToolEffectSummary{Status: domain.ToolEffectCommitted},
		"ModelRequestEnvelope":    domain.ModelRequestEnvelope{},
		"ModelRequestCapture":     domain.ModelRequestCapture{Mode: domain.ModelRequestCaptureMetadata},
		"RuntimeInvariantFailure": domain.RuntimeInvariantFailure{Code: "sequence_gap", Owner: "run", RunID: run.ID, Message: "gap"},
	} {
		t.Run(name+"/serialized-defaults", func(t *testing.T) {
			encoded, err := json.Marshal(fixture)
			if err != nil {
				t.Fatal(err)
			}
			contract.validate(t, "/components/schemas/"+name, encoded)
		})
	}
	for _, name := range []string{"TaskStateRevision", "RunProjectionSnapshot", "ModelRequestDebugResponse", "RecoverySummary"} {
		t.Run(name+"/reject-missing-identity", func(t *testing.T) {
			schema, err := contract.compiler.Compile("https://agentflow.test/openapi#/components/schemas/" + name)
			if err != nil {
				t.Fatal(err)
			}
			if schema.Validate(map[string]any{}) == nil {
				t.Fatal("missing fields accepted")
			}
		})
	}
}

// Failure inventory: match/no-match bodies retain pipeline diagnostics, and a
// malformed candidate list cannot satisfy the generated Search response contract.
func TestRAGSearchResponseContract(t *testing.T) {
	contract := loadResourceContract(t)
	fixture := newPipelineRegressionFixture(t)
	fixture.seedDocument(t)
	for _, query := range []string{"alpha-4242 recovery protocol", "zebra astronomy portfolio"} {
		response := fixture.request(t, http.MethodPost, "/api/rag/search", `{"query":`+quotedJSON(t, query)+`}`)
		if response.Code != http.StatusOK {
			t.Fatalf("search: %d %s", response.Code, response.Body.String())
		}
		contract.response(t, http.MethodPost, "/api/rag/search", response)
	}
	schema, err := contract.compiler.Compile("https://agentflow.test/openapi#/paths/~1api~1rag~1search/post/responses/200/content/application~1json/schema")
	if err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []map[string]any{{"items": "not a list"}, {"items": []any{map[string]any{"chunk": "invalid"}}}} {
		if schema.Validate(invalid) == nil {
			t.Fatalf("invalid Search response accepted: %+v", invalid)
		}
	}
}
