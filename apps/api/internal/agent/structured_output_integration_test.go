package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/failure"
	"agentflow-platform/apps/api/internal/inference/openai"
	"agentflow-platform/apps/api/internal/inference/provider"
	"agentflow-platform/apps/api/internal/inference/routing"
	"agentflow-platform/apps/api/internal/testsupport/fixturestore"
	"agentflow-platform/apps/api/internal/verification"
)

// Resume must use the frozen schema and reuse completed side-effect stages.
// A capability boolean without a wire format must reject before any HTTP call.
func TestStructuredOutputResumeAndUnsupportedRoute(t *testing.T) {
	for _, scenario := range []struct {
		mode         string
		supported    bool
		autonomous   bool
		refusal      bool
		smallContext bool
	}{{"strict", false, false, false, false}, {"strict", true, false, false, false}, {"local", false, false, false, false}, {"strict", true, true, false, false}, {"strict", true, true, true, false}, {"strict", true, false, false, true}} {
		supported := scenario.supported
		t.Run(fmt.Sprintf("%s/supported=%t/loop=%t/refusal=%t", scenario.mode, supported, scenario.autonomous, scenario.refusal), func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				var input struct {
					ResponseFormat struct {
						Schema struct {
							Schema map[string]any `json:"schema"`
							Strict bool           `json:"strict"`
						} `json:"json_schema"`
					} `json:"response_format"`
				}
				if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
					t.Error(err)
					w.WriteHeader(400)
					return
				}
				encoded, _ := json.Marshal(input.ResponseFormat.Schema.Schema)
				if scenario.mode == "strict" && (!input.ResponseFormat.Schema.Strict || !strings.Contains(string(encoded), "summary") || strings.Contains(string(encoded), "CHANGED")) {
					t.Errorf("frozen schema lost: %s", encoded)
					w.WriteHeader(400)
					return
				}
				if scenario.mode == "local" && input.ResponseFormat.Schema.Strict {
					t.Error("local fallback sent a strict constraint")
					w.WriteHeader(400)
					return
				}
				if scenario.refusal {
					_, _ = w.Write([]byte(`{"choices":[{"message":{"refusal":"fixture refuses"},"finish_reason":"stop"}]}`))
					return
				}
				_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"summary\":\"ok\"}"},"finish_reason":"stop"}]}`))
			}))
			defer server.Close()
			storage := fixturestore.New()
			client := openai.NewClient("fixture", server.URL, "fixture")
			identity := client.RuntimeIdentity()
			caps := routing.Capabilities{Streaming: true, StructuredOutput: true, ToolCalling: true}
			if supported {
				caps.StructuredOutputFormat = provider.OutputFormatOpenAIJSONSchema
			}
			window := 128000
			if scenario.smallContext {
				window = 10000
			}
			catalog, err := routing.NewCatalog(routing.Binding{Client: client, Descriptor: routing.Descriptor{ID: "fixture", Provider: identity.Provider, Model: identity.Model, Endpoint: identity.BaseURL, Capabilities: caps, ContextWindowTokens: window, MaxOutputTokens: 8192, Pricing: routing.Pricing{Source: "fixture"}}})
			if err != nil {
				t.Fatal(err)
			}
			runtime := newRuntimeForTest(RuntimeOptions{Store: storage, ModelRoutes: catalog, RouterMode: RouterModeQuery}, client)
			contract, err := verification.NewRegistry(verification.Options{}).FreezeContract(&domain.CompletionContract{Output: &domain.StructuredOutputConfig{Mode: scenario.mode, VerifierID: "report"}, Verifiers: []domain.VerifierSpec{{ID: "report", Type: domain.VerifierJSONSchema, Required: true, Config: map[string]any{"schema": map[string]any{"type": "object", "properties": map[string]any{"summary": map[string]any{"type": "string"}}, "required": []any{"summary"}, "additionalProperties": false}}}}})
			if err != nil {
				t.Fatal(err)
			}
			if scenario.smallContext {
				contract.Verifiers[0].Config["schema"].(map[string]any)["description"] = strings.Repeat("x", 4096)
				contract, err = verification.NewRegistry(verification.Options{}).FreezeContract(contract)
				if err != nil {
					t.Fatal(err)
				}
			}
			conversation, err := storage.CreateConversation("structured recovery")
			if err != nil {
				t.Fatal(err)
			}
			if scenario.autonomous {
				prepared, err := runtime.PrepareAutonomousRunWithContract(context.Background(), "agent_planner", conversation.ID, contract)
				if err != nil {
					t.Fatal(err)
				}
				events := make(chan domain.RunEvent, 64)
				err = runtime.emitAutonomousFinal(context.Background(), events, prepared, 0, "done", "candidate from completed Act")
				if scenario.refusal {
					if info := failure.Describe(err); info.Code != "content_policy" {
						t.Fatalf("final refusal lost: %+v %v", info, err)
					}
					return
				}
				if err != nil || requests.Load() != 1 {
					t.Fatalf("final generation: %v calls=%d", err, requests.Load())
				}
				steps, err := storage.ListCollaborationSteps(prepared.Run.ID)
				if err != nil || len(steps) != 1 || steps[0].Role != "final" || steps[0].Output != `{"summary":"ok"}` {
					t.Fatalf("final stage not durable: %+v %v", steps, err)
				}
				return
			}
			prepared, err := runtime.PrepareCollaborationRunWithContract(context.Background(), "agent_planner", conversation.ID, contract)
			if err != nil {
				t.Fatal(err)
			}
			for _, role := range []string{"planner", "router", "worker", "reviewer"} {
				id := "agent_planner"
				if role == "reviewer" {
					id = ""
				}
				createCompletedStage(t, runtime, prepared.Run, role, id, "authorized material", "durable result")
			}
			if _, err := storage.UpdateRunStatus(prepared.Run.ID, domain.RunFailedRecoverable, "process stopped before finalization"); err != nil {
				t.Fatal(err)
			}
			contract.Verifiers[0].Config["schema"] = map[string]any{"type": "object", "properties": map[string]any{"CHANGED": map[string]any{"type": "string"}}}
			events, errs := runtime.ResumeRecoverableCollaboration(context.Background(), prepared.Run.ID)
			for range events {
			}
			err = <-errs
			if (!supported || scenario.smallContext) && scenario.mode == "strict" {
				if failure.Describe(err).Code != "model_route_unavailable" || requests.Load() != 0 {
					t.Fatalf("unsupported route called provider: %v calls=%d", err, requests.Load())
				}
				return
			}
			if err != nil || requests.Load() != 1 {
				t.Fatalf("resume: %v calls=%d", err, requests.Load())
			}
			steps, err := storage.ListCollaborationSteps(prepared.Run.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(steps) != 5 || steps[4].Output != `{"summary":"ok"}` {
				t.Fatalf("resume reran a committed stage or lost result: %+v", steps)
			}
		})
	}
}
