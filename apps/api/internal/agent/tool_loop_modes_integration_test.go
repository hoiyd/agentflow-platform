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
	"time"

	"agentflow-platform/apps/api/internal/domain"
	eventpkg "agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/inference/capture"
	"agentflow-platform/apps/api/internal/inference/openai"
	"agentflow-platform/apps/api/internal/inference/provider"
	"agentflow-platform/apps/api/internal/testsupport/fixturestore"
	"agentflow-platform/apps/api/internal/testsupport/modelstream"
)

func TestBoundedToolLoopAcrossExecutionModes(t *testing.T) {
	for _, mode := range []string{ChatModeSingle, ChatModeMultiAgent, ChatModeAutonomous} {
		t.Run(mode, func(t *testing.T) {
			var physical, toolRounds atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				physical.Add(1)
				var input struct {
					Messages []provider.Message `json:"messages"`
					Tools    []any              `json:"tools"`
					Stream   bool               `json:"stream"`
				}
				if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
					t.Error(err)
					w.WriteHeader(400)
					return
				}
				content, reason := "5", "stop"
				var calls []provider.ToolCall
				if len(input.Tools) > 0 {
					if mode == ChatModeAutonomous && !strings.Contains(input.Messages[0].Content, "Act stage") {
						t.Error("Act role prompt lost")
					}
					if mode == ChatModeMultiAgent && !strings.Contains(input.Messages[0].Content, "Worker collaboration role") {
						t.Error("Worker role prompt lost")
					}
					observations := 0
					for _, message := range input.Messages {
						if message.Role == "tool" {
							observations++
						}
					}
					toolRounds.Add(1)
					if observations < 2 {
						expression := "1 + 1"
						if observations == 1 {
							last := input.Messages[len(input.Messages)-1]
							if !strings.Contains(last.Content, `"value":2`) {
								t.Error("dependency result missing")
							}
							expression = "2 + 3"
						}
						arguments, _ := json.Marshal(map[string]string{"expression": expression})
						content, reason = "not final commentary", "tool_calls"
						calls = []provider.ToolCall{{ID: "provider-reused-id", Type: "function", Function: provider.FunctionCall{Name: "calculator", Arguments: string(arguments)}}}
					}
				} else if len(input.Messages) > 0 && strings.Contains(input.Messages[0].Content, "Decide stage") {
					content = `{"decision":"stop","reason":"calculation complete","final_answer":"5"}`
				}
				response := map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": content, "tool_calls": calls}, "finish_reason": reason}}, "usage": map[string]int{"prompt_tokens": 10, "completion_tokens": 2, "total_tokens": 12}}
				if len(input.Tools) > 0 && !input.Stream {
					t.Error("Tool mode did not request streaming")
				}
				if input.Stream {
					stream, err := modelstream.Completion(response)
					if err != nil {
						t.Error(err)
						w.WriteHeader(500)
						return
					}
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprint(w, stream)
				} else {
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(response)
				}
			}))
			t.Cleanup(server.Close)
			storage := fixturestore.New()
			persona, err := storage.CreateAgent(domain.Agent{Name: "Calculator", Description: "calculator arithmetic", SystemPrompt: "Use calculator for arithmetic.", Tools: []string{"calculator"}})
			if err != nil {
				t.Fatal(err)
			}
			conversation, err := storage.CreateConversation("multi-round " + mode)
			if err != nil {
				t.Fatal(err)
			}
			client := openai.NewClientWithTimeout("fixture-not-a-secret", server.URL, "fixture-model", time.Second)
			client.SetRequestRecorder(capture.NewRecorder(storage, capture.Options{Mode: domain.ModelRequestCaptureFull}))
			runtime := NewRuntime(RuntimeOptions{Store: storage, ModelClient: client, EmbeddingClient: newLocalFallbackOpenAIClientForTest(), RouterMode: RouterModeQuery, RunBudget: domain.RuntimeRunBudget{MaxModelCalls: 12, MaxToolCalls: 4, MaxRuntimeMS: 10000}, Autonomous: AutonomousLimits{MaxIterations: 1}})
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			const task = "Use calculator to calculate 1 + 1, then add 3 to that result."
			var runID, output string
			drain := func(events <-chan domain.RunEvent, errs <-chan error) {
				for item := range events {
					if item.Type == domain.EventModelDelta {
						if reset, _ := item.Payload["reset"].(bool); reset {
							output = ""
						}
						output += fmt.Sprint(item.Payload["delta"])
					}
				}
				if err := <-errs; err != nil {
					t.Fatal(err)
				}
			}
			switch mode {
			case ChatModeSingle:
				prepared, err := runtime.PrepareChatRunWithContract(ctx, persona.ID, conversation.ID, nil)
				if err != nil {
					t.Fatal(err)
				}
				runID = prepared.Run.ID
				drain(runtime.StreamChat(ctx, prepared, nil, task))
			case ChatModeMultiAgent:
				prepared, err := runtime.PrepareCollaborationRunWithContract(ctx, persona.ID, conversation.ID, nil)
				if err != nil {
					t.Fatal(err)
				}
				runID = prepared.Run.ID
				drain(runtime.RunCollaboration(ctx, prepared, task))
				drain(runtime.ContinueCollaboration(ctx, runID, "Use calculator to calculate twice.", domain.AgentRoutingRequirements{RequiredTools: []string{"calculator"}}))
			case ChatModeAutonomous:
				prepared, err := runtime.PrepareAutonomousRunWithContract(ctx, persona.ID, conversation.ID, nil)
				if err != nil {
					t.Fatal(err)
				}
				runID = prepared.Run.ID
				drain(runtime.RunAutonomous(ctx, prepared, task))
			}
			if output != "5" || toolRounds.Load() != 3 {
				t.Fatalf("mode=%s final=%q Tool rounds=%d", mode, output, toolRounds.Load())
			}
			ledger, _, err := storage.GetRunUsageLedger(runID)
			if err != nil || ledger.Totals.ModelCalls != int(physical.Load()) || ledger.Totals.ToolCalls != 2 || ledger.Totals.OpenReservations != 0 {
				t.Fatalf("ledger=%#v HTTP=%d err=%v", ledger.Totals, physical.Load(), err)
			}
			items, _ := storage.ListRunEvents(runID)
			if err := eventpkg.ValidateLifecycle(items); err != nil {
				t.Fatal(err)
			}
			records, _ := storage.ListModelRequestRecords(runID)
			if len(records) != int(physical.Load()) {
				t.Fatalf("capture=%d HTTP=%d", len(records), physical.Load())
			}
			t.Logf("mode=%s run=%s logical_calls=%d physical_attempts=%d Tool_calls=2 outcome=5 protocol=paired limitations=deterministic-provider", mode, runID, ledger.Totals.ModelCalls, physical.Load())
		})
	}
}
