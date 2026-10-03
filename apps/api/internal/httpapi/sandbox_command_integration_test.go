package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentpkg "agentflow-platform/apps/api/internal/agent"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/inference/openai"
	"agentflow-platform/apps/api/internal/inference/provider"
	"agentflow-platform/apps/api/internal/sandbox"
	"agentflow-platform/apps/api/internal/testsupport/modelstream"
	"agentflow-platform/apps/api/internal/tool"
	"agentflow-platform/apps/api/internal/tool/policy"
)

func TestSandboxCommandHTTPTraceAndReceiptsAcrossModes(t *testing.T) {
	for _, mode := range []string{"single", "multi_agent", "autonomous"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			cli := filepath.Join(root, "sbx-fixture")
			if err := os.WriteFile(cli, []byte("#!/bin/sh\ncase \"$1\" in exec) printf 'scratch fixture receipt';; esac\n"), 0700); err != nil {
				t.Fatal(err)
			}
			runner, err := sandbox.New(sandbox.Options{Executable: cli, StateDirectory: filepath.Join(root, "state"), AllowedCommands: []string{"/bin/sh"}})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = runner.Close(context.Background()) })
			binding := tool.SandboxCommandTool(runner)
			cfg := tool.DefaultConfig()
			cfg.EnabledTools = append(cfg.EnabledTools, binding.Descriptor.Name)
			cfg.SecurityPolicy.Rules = append(cfg.SecurityPolicy.Rules, policy.Rule{ID: "sandbox-fixture", Tool: binding.Descriptor.Name, Action: policy.ActionAllowAndLog, Capability: binding.Descriptor.Security})
			path := filepath.Join(root, "tools.json")
			if err := tool.SaveConfig(path, cfg); err != nil {
				t.Fatal(err)
			}
			manager, err := tool.NewManager(path, binding)
			if err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					Messages []provider.Message `json:"messages"`
					Tools    []any              `json:"tools"`
					Stream   bool               `json:"stream"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					w.WriteHeader(400)
					return
				}
				content, reason := "Run the scratch-fixture script.", "stop"
				var calls []provider.ToolCall
				if len(request.Tools) > 0 {
					found := false
					for _, message := range request.Messages {
						if message.Role == "tool" {
							found = true
							if !strings.Contains(message.Content, "scratch fixture receipt") || !strings.Contains(message.Content, `"cleanup_confirmed":true`) {
								t.Errorf("invalid command receipt: %s", message.Content)
							}
						}
					}
					if !found {
						content, reason = "", "tool_calls"
						calls = []provider.ToolCall{{ID: "sandbox-call", Type: "function", Function: provider.FunctionCall{Name: "sandbox_command", Arguments: `{"args":["/bin/sh","-c","printf scratch"]}`}}}
					} else {
						content = "Scratch receipt verified."
					}
				}
				if len(request.Messages) > 0 && strings.Contains(request.Messages[0].Content, "Decide stage") {
					content = `{"decision":"stop","reason":"scratch receipt received","final_answer":"Scratch receipt verified."}`
				}
				response := map[string]any{"model": "sandbox-fixture-model", "choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": content, "tool_calls": calls}, "finish_reason": reason}}, "usage": map[string]int{"prompt_tokens": 20, "completion_tokens": 10, "total_tokens": 30}}
				if request.Stream {
					stream, err := modelstream.Completion(response)
					if err != nil {
						t.Error(err)
						w.WriteHeader(500)
						return
					}
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprint(w, stream)
				} else {
					writeJSON(w, 200, response)
				}
			}))
			t.Cleanup(server.Close)
			dependencies := completeHandlerDependencies(t)
			storage := fullStoreForTest(t, dependencies)
			selected, err := storage.CreateAgent(domain.Agent{Name: "Scratch runner", Description: "Run scratch-fixture scripts", SystemPrompt: "Use sandbox_command for scratch-fixture tasks.", Tools: []string{"sandbox_command"}, RoutingHints: domain.AgentRoutingHints{Capabilities: []string{"scratch-fixture", "script", "run"}}})
			if err != nil {
				t.Fatal(err)
			}
			client := openai.NewClientWithTimeout("fixture-not-a-secret", server.URL, "sandbox-fixture-model", 5*time.Second)
			dependencies.Tools = manager
			dependencies.AgentRuntime = newRuntimeForTest(agentpkg.RuntimeOptions{Store: storage, Tools: manager, ToolExecution: tool.ExecutorOptions{EffectJournal: storage, ArtifactStore: storage}, RouterMode: agentpkg.RouterModeQuery, Autonomous: agentpkg.AutonomousLimits{MaxIterations: 1}, RunBudget: domain.RuntimeRunBudget{MaxModelCalls: 16, MaxToolCalls: 4, MaxRuntimeMS: 20000}}, client)
			handler, err := NewHandler(dependencies)
			if err != nil {
				t.Fatal(err)
			}
			fixture := &pipelineRegressionFixture{routes: handler.Routes(), store: storage}
			chat := fixture.request(t, http.MethodPost, "/api/chat", fmt.Sprintf(`{"message":"Run the scratch-fixture script.","mode":%q,"agent_id":%q}`, mode, selected.ID))
			if chat.Code != 200 || !strings.Contains(chat.Body.String(), "event: done") {
				t.Fatalf("chat: %d %s", chat.Code, chat.Body.String())
			}
			runs, err := storage.ListRunsByWorkspace(pipelineRegressionWorkspace)
			if err != nil || len(runs) != 1 {
				t.Fatalf("runs: %+v %v", runs, err)
			}
			if mode == "multi_agent" {
				response := fixture.request(t, http.MethodPost, "/api/runs/"+runs[0].ID+"/continue", `{"plan":"Run the scratch-fixture script.","routing_requirements":{"preferred_capabilities":["scratch-fixture"]}}`)
				if response.Code != 200 {
					t.Fatalf("continue: %s", response.Body.String())
				}
			}
			response := fixture.request(t, http.MethodGet, "/api/runs/"+runs[0].ID+"/replay", "")
			var replay domain.RunReplay
			if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &replay) != nil || replay.Run.Status != domain.RunCompleted {
				t.Fatalf("replay: %d %s", response.Code, response.Body.String())
			}
			effects, err := storage.ListToolEffects(replay.Run.ID)
			if err != nil || len(effects) == 0 {
				t.Fatalf("no durable command receipt: %+v %v", effects, err)
			}
			for _, effect := range effects {
				if effect.ToolName == "sandbox_command" && (effect.Status != domain.ToolEffectCommitted || (effect.TurnID == "" && effect.StageID == "")) {
					t.Fatalf("invalid command ownership: %+v", effect)
				}
			}
			if !strings.Contains(string(response.Body.Bytes()), "scratch fixture receipt") || !strings.Contains(string(response.Body.Bytes()), "sandbox_id") {
				t.Fatal("receipt not inspectable in persisted Replay")
			}
			t.Logf("sandbox_http_evidence mode=%s run=%s effects=%d limitation=controlled-cli-and-fixture-store-not-vm-isolation", mode, replay.Run.ID, len(effects))
		})
	}
}
