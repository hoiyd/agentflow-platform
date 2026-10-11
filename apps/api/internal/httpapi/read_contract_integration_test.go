package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"
)

// Validate serialized handler responses, not parallel copies of transport DTOs.
func TestConsumedReadContracts(t *testing.T) {
	data, err := os.ReadFile("../../../../api/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var spec map[string]any
	if err := yaml.Unmarshal(data, &spec); err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	const resource = "https://agentflow.test/openapi"
	if err := compiler.AddResource(resource, spec); err != nil {
		t.Fatal(err)
	}
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
			schema, err := compiler.Compile(resource + "#/components/schemas/" + c.schema)
			if err != nil {
				t.Fatal(err)
			}
			response := httptest.NewRecorder()
			h.ServeHTTP(response, httptest.NewRequest(c.method, c.path, strings.NewReader(c.body)))
			if response.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			var wire any
			if err := json.Unmarshal(response.Body.Bytes(), &wire); err != nil {
				t.Fatal(err)
			}
			if err := schema.Validate(wire); err != nil {
				t.Fatalf("response violates contract: %v", err)
			}
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
			schema, err := compiler.Compile(resource + "#/components/schemas/" + name)
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(fixture)
			if err != nil {
				t.Fatal(err)
			}
			var wire any
			if err := json.Unmarshal(encoded, &wire); err != nil {
				t.Fatal(err)
			}
			if err := schema.Validate(wire); err != nil {
				t.Fatalf("default/null fields violate contract: %v", err)
			}
		})
	}
	for _, name := range []string{"TaskStateRevision", "RunProjectionSnapshot", "ModelRequestDebugResponse", "RecoverySummary"} {
		t.Run(name+"/reject-missing-identity", func(t *testing.T) {
			schema, err := compiler.Compile(resource + "#/components/schemas/" + name)
			if err != nil {
				t.Fatal(err)
			}
			if schema.Validate(map[string]any{}) == nil {
				t.Fatal("missing fields accepted")
			}
		})
	}
}
