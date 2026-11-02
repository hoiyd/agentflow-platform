package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/agent/toolloop"
	"agentflow-platform/apps/api/internal/agent/turn"
	"agentflow-platform/apps/api/internal/contextassembly"
	"agentflow-platform/apps/api/internal/domain"
	eventpkg "agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/inference/openai"
	"agentflow-platform/apps/api/internal/inference/routing"
	"agentflow-platform/apps/api/internal/testsupport/fixturestore"
	"agentflow-platform/apps/api/internal/testsupport/modelstream"
	"agentflow-platform/apps/api/internal/tool"
)

func TestEffectiveToolSchemaRoutingMatchesWire(t *testing.T) {
	catalog := modelToolSchemaCatalog(t)
	full, err := json.Marshal(catalog.Definitions())
	if err != nil {
		t.Fatal(err)
	}
	fullTokens := contextassembly.EstimateTokens(string(full))
	if fullTokens <= 4096 || toolloop.InitialSchemaTokens(catalog, domain.ToolSchemaConfig{Mode: "lazy"}) >= 2048 {
		t.Fatal("fixture must distinguish full, lazy, default threshold and narrow-route capacity")
	}
	small, err := tool.NewCatalog(tool.CalculatorTool())
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{ChatModeSingle, ChatModeMultiAgent, ChatModeAutonomous} {
		for _, role := range []string{"planner", "answer", "worker", "act"} {
			for _, tc := range []struct {
				name    string
				config  *domain.ToolSchemaConfig
				lazy    bool
				small   bool
				noRoute bool
			}{
				{name: "missing"},
				{name: "default", config: &domain.ToolSchemaConfig{}},
				{name: "eager", config: &domain.ToolSchemaConfig{Mode: "eager", SchemaTokenThreshold: 1}},
				{name: "lazy", config: &domain.ToolSchemaConfig{Mode: "lazy", SchemaTokenThreshold: fullTokens + 1}, lazy: true},
				{name: "auto_default", config: &domain.ToolSchemaConfig{Mode: "auto"}, lazy: true},
				{name: "auto_below_threshold", config: &domain.ToolSchemaConfig{Mode: "auto", SchemaTokenThreshold: fullTokens + 1}},
				{name: "auto_at_threshold", config: &domain.ToolSchemaConfig{Mode: "auto", SchemaTokenThreshold: fullTokens}, lazy: true},
				{name: "auto_above_threshold", config: &domain.ToolSchemaConfig{Mode: "auto", SchemaTokenThreshold: fullTokens - 1}, lazy: true},
				{name: "auto_expensive_index", config: &domain.ToolSchemaConfig{Mode: "auto", SchemaTokenThreshold: 1}, small: true},
				{name: "narrow_route", config: &domain.ToolSchemaConfig{Mode: "lazy"}, lazy: true, noRoute: role == "planner"},
			} {
				t.Run(mode+"/"+role+"/"+tc.name, func(t *testing.T) {
					tools := catalog
					if tc.small {
						tools = small
					}
					definitions, _ := json.Marshal(tools.Definitions())
					wantLazy := tc.lazy && role != "planner"
					var calls atomic.Int32
					var wireSchemaTokens atomic.Int32
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						calls.Add(1)
						var input struct {
							Model      string           `json:"model"`
							Tools      []map[string]any `json:"tools"`
							Stream     bool             `json:"stream"`
							ToolChoice string           `json:"tool_choice"`
						}
						if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
							t.Error(err)
							http.Error(w, "invalid JSON", http.StatusBadRequest)
							return
						}
						valid := !tc.noRoute && r.URL.Path == "/v1/chat/completions" && input.Model == "schema-fixture" && input.Stream && input.ToolChoice == "auto"
						if wantLazy {
							valid = valid && len(input.Tools) == 1
							if len(input.Tools) == 1 {
								function, _ := input.Tools[0]["function"].(map[string]any)
								parameters, _ := json.Marshal(function["parameters"])
								wantParameters, _ := json.Marshal(tool.ObjectSchema(map[string]any{"query": map[string]any{"type": "string", "minLength": 1, "maxLength": 256}}, []string{"query"}))
								valid = valid && input.Tools[0]["type"] == "function" && function["name"] == "tool_search" && string(parameters) == string(wantParameters)
								if _, installed := tools.Installed("tool_search"); installed {
									t.Error("runtime discovery leaked into frozen candidates")
								}
							}
						} else {
							valid = valid && reflect.DeepEqual(input.Tools, tools.Definitions())
						}
						encoded, _ := json.Marshal(input.Tools)
						wireSchemaTokens.Store(int32(contextassembly.EstimateTokens(string(encoded))))
						if !valid {
							t.Errorf("strict provider rejected schemas: lazy=%t tools=%d no_route=%t", wantLazy, len(input.Tools), tc.noRoute)
							http.Error(w, "schema policy mismatch", http.StatusBadRequest)
							return
						}
						stream, err := modelstream.Completion(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "ok"}, "finish_reason": "stop"}}, "usage": map[string]int{"prompt_tokens": 10, "completion_tokens": 1, "total_tokens": 11}})
						if err != nil {
							t.Error(err)
							w.WriteHeader(http.StatusInternalServerError)
							return
						}
						w.Header().Set("Content-Type", "text/event-stream")
						fmt.Fprint(w, stream)
					}))
					t.Cleanup(server.Close)
					client := openai.NewClientWithTimeout("fixture-not-a-secret", server.URL+"/v1", "schema-fixture", time.Second)
					identity := client.RuntimeIdentity()
					window := 128000
					if tc.name == "narrow_route" {
						window = 4096
					}
					routes, err := routing.NewCatalog(routing.Binding{Client: client, Descriptor: routing.Descriptor{
						ID: "schema-route", Provider: identity.Provider, Model: identity.Model, Endpoint: identity.BaseURL,
						Capabilities:        routing.Capabilities{ToolCalling: true, Streaming: true, StructuredOutput: true},
						ContextWindowTokens: window, MaxOutputTokens: 100, Pricing: routing.Pricing{Source: "fixture"},
					}})
					if err != nil {
						t.Fatal(err)
					}
					storage := fixturestore.New()
					agent, err := storage.CreateAgent(domain.Agent{Name: "Schema fixture", SystemPrompt: "help", Tools: tools.EnabledNames()})
					if err != nil {
						t.Fatal(err)
					}
					conversation, err := storage.CreateConversation("effective schema")
					if err != nil {
						t.Fatal(err)
					}
					manager, err := tool.NewManager("")
					if err != nil {
						t.Fatal(err)
					}
					manager.SetWorkspaceResolver(func(string, *tool.Catalog) (*tool.Catalog, error) { return tools, nil })
					runtime := newRuntimeForTest(RuntimeOptions{
						Store: storage, ModelRoutes: routes, RouterMode: RouterModeQuery, Tools: manager,
						ContextAssembly: domain.ContextAssemblyConfig{ContextWindowTokens: 128000, OutputReserveTokens: 100, SafetyMarginTokens: 50, CompactionMode: contextassembly.CompactionModeOff},
						RunBudget:       domain.RuntimeRunBudget{MaxModelCalls: 1},
					}, client)
					snapshot, err := runtime.captureRuntimeSnapshot(mode, agent, []domain.Agent{agent})
					if err != nil {
						t.Fatal(err)
					}
					if tc.config == nil {
						snapshot.ToolSchema = nil
					} else {
						config := tc.config.Normalize()
						snapshot.ToolSchema = &config
					}
					run, err := storage.CreateRunWithContract(agent.ID, conversation.ID, snapshot, nil)
					if err != nil {
						t.Fatal(err)
					}
					request := turn.Request{RunID: run.ID, ConversationID: conversation.ID, TurnID: "schema-turn", Agent: agent, Role: role,
						Input: "Produce a report", ModelMode: turn.ModelModeAgentStream, Catalog: tools, Sink: runtime.runEventSink()}
					if mode != ChatModeSingle {
						stage, err := storage.CreateCollaborationStep(domain.CollaborationStep{RunID: run.ID, ConversationID: conversation.ID, AgentID: agent.ID, Role: role, Status: domain.CollaborationStepRunning, Input: request.Input})
						if err != nil {
							t.Fatal(err)
						}
						request.StepID = stage.ID
					}
					ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
					defer cancel()
					ctx = eventpkg.WithScope(ctx, eventpkg.Scope{RunID: run.ID, ConversationID: conversation.ID, StageID: request.StepID, TurnID: request.TurnID})
					result, executeErr := (runtimeTurnModel{runtime: runtime}).Execute(ctx, request, func(turn.ModelEvent) {})
					items, err := storage.ListRunEvents(run.ID)
					if err != nil {
						t.Fatal(err)
					}
					i := slices.IndexFunc(items, func(item domain.RunEvent) bool { return item.Type == domain.EventModelRouteDecided })
					if i < 0 {
						t.Fatalf("missing routing evidence: %v", executeErr)
					}
					decision := items[i]
					config, _ := json.Marshal(snapshot.ToolSchema)
					recordedRequirements, _ := json.Marshal(decision.Payload["requirements"])
					t.Logf("run=%s stage=%s turn=%s model=schema-fixture mode=%s role=%s config=%s requirements=%s full_schema_tokens=%d wire_schema_tokens=%d calls=%d route=%v output=%q error=%v input=%q budget=max_model_calls:1 limits=backend-Turn/local-HTTP/in-memory/no-browser/no-live-model", run.ID, request.StepID, request.TurnID, mode, role, config, recordedRequirements, contextassembly.EstimateTokens(string(definitions)), wireSchemaTokens.Load(), calls.Load(), decision.Payload["outcome"], result.Output, executeErr, request.Input)
					if tc.noRoute {
						if !errors.Is(executeErr, routing.ErrNoCompatibleRoute) || calls.Load() != 0 || decision.Payload["outcome"] != "no_compatible_route" {
							t.Fatalf("effective eager schema bypassed route capacity: calls=%d outcome=%v err=%v", calls.Load(), decision.Payload["outcome"], executeErr)
						}
					} else {
						if executeErr != nil || result.Output != "ok" || calls.Load() != 1 {
							t.Fatalf("output=%q calls=%d err=%v", result.Output, calls.Load(), executeErr)
						}
						base := contextassembly.EstimateTokens(agent.SystemPrompt) + contextassembly.EstimateTokens(request.Input)
						requirements := modelRequirements(request, &snapshot)
						if got := requirements.EstimatedInputTokens - base; got != int(wireSchemaTokens.Load()) {
							t.Errorf("routing schema estimate=%d, serialized wire schema=%d", got, wireSchemaTokens.Load())
						}
						var recorded routing.Requirements
						if err := json.Unmarshal(recordedRequirements, &recorded); err != nil || recorded != requirements {
							t.Fatalf("routing event lost effective requirements: %s err=%v", recordedRequirements, err)
						}
					}
				})
			}
		}
	}
}
