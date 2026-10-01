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
	"unicode/utf8"

	"agentflow-platform/apps/api/internal/domain"
	eventpkg "agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/inference/capture"
	"agentflow-platform/apps/api/internal/inference/openai"
	"agentflow-platform/apps/api/internal/inference/provider"
	"agentflow-platform/apps/api/internal/inference/routing"
	"agentflow-platform/apps/api/internal/testsupport/fixturestore"
	"agentflow-platform/apps/api/internal/testsupport/modelstream"
)

func TestBoundedToolLoopAcrossExecutionModes(t *testing.T) {
	for _, mode := range []string{ChatModeSingle, ChatModeMultiAgent, ChatModeAutonomous} {
		for _, display := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/display=%t", mode, display), func(t *testing.T) {
				reasoningForRound := func(round int) string {
					prefix := "DISPLAY_ONLY_REASONING sk-fixtureCredential123456 fixture-not-a-secret "
					switch round {
					case 0:
						return prefix + strings.Repeat("\u601d", 6000)
					case 1:
						return prefix + "-----BEGIN PRIVATE KEY-----\nPRIVATE_KEY_BODY"
					default:
						return prefix + "-----BEGIN PRIVATE KEY-----\nPRIVATE_KEY_BODY\n-----END PRIVATE KEY-----"
					}
				}
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
					observations := 0
					var calls []provider.ToolCall
					if len(input.Tools) > 0 {
						if mode == ChatModeAutonomous && !strings.Contains(input.Messages[0].Content, "Act stage") {
							t.Error("Act role prompt lost")
						}
						if mode == ChatModeMultiAgent && !strings.Contains(input.Messages[0].Content, "Worker collaboration role") {
							t.Error("Worker role prompt lost")
						}
						for _, message := range input.Messages {
							if message.Role == "tool" {
								observations++
							}
							if display && message.Role == "assistant" && len(message.ToolCalls) > 0 && (message.ReasoningContent == nil || *message.ReasoningContent != reasoningForRound(observations)) {
								t.Error("display filtering changed serialized continuation")
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
					if display && input.Stream {
						response["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)["reasoning_content"] = reasoningForRound(observations)
					}
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
				options := RuntimeOptions{Store: storage, EmbeddingClient: newLocalFallbackOpenAIClientForTest(), RouterMode: RouterModeQuery, RunBudget: domain.RuntimeRunBudget{MaxModelCalls: 12, MaxToolCalls: 4, MaxRuntimeMS: 10000}, Autonomous: AutonomousLimits{MaxIterations: 1}}
				if display {
					client.SetRequestRecorder(capture.NewRecorder(storage, capture.Options{Mode: domain.ModelRequestCaptureMetadata}))
					identity := client.RuntimeIdentity()
					options.ModelRoutes, err = routing.NewCatalog(routing.Binding{Client: client, Descriptor: routing.Descriptor{
						ID: "reasoning", Provider: identity.Provider, Model: identity.Model, Endpoint: identity.BaseURL,
						Capabilities:        routing.Capabilities{ToolCalling: true, StructuredOutput: true, Streaming: true, ReasoningDisplayFormat: provider.ReasoningFormatDeepSeek},
						ContextWindowTokens: 128000, MaxOutputTokens: 8192, Pricing: routing.Pricing{Source: "fixture"},
					}})
					if err != nil {
						t.Fatal(err)
					}
				}
				runtime := newRuntimeForTest(options, client)
				ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
				defer cancel()
				const task = "Use calculator to calculate 1 + 1, then add 3 to that result."
				var runID, output string
				var reasoningEvents []domain.RunEvent
				var liveReasoning []domain.RunEvent
				drain := func(events <-chan domain.RunEvent, errs <-chan error) {
					for item := range events {
						if item.Type == domain.EventModelReasoning {
							reasoningEvents = append(reasoningEvents, item)
						}
						if item.Type == domain.EventModelReasoningDelta {
							liveReasoning = append(liveReasoning, item)
						}
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
				if display {
					if len(liveReasoning) == 0 {
						t.Fatal("no live reasoning batches across Tool rounds")
					}
					for _, item := range liveReasoning {
						text, _ := item.Payload["text"].(string)
						if item.RunID != runID || item.TurnID == "" || (mode != ChatModeSingle && item.StageID == "") || item.Payload["status"] != "receiving" || text == "" || strings.Contains(text, "sk-fixture") || strings.Contains(text, "fixture-not-a-secret") || strings.Contains(text, "PRIVATE_KEY_BODY") {
							t.Fatalf("unsafe or unscoped live reasoning: %#v", item)
						}
					}
					if len(reasoningEvents) != 6 {
						t.Fatalf("reasoning events=%d", len(reasoningEvents))
					}
					calls := map[string]bool{}
					for i, item := range reasoningEvents {
						if item.RunID != runID || item.TurnID == "" || (mode != ChatModeSingle && item.StageID == "") {
							t.Fatalf("lost scope: %#v", item)
						}
						call, _ := item.Payload["model_call_id"].(string)
						if call == "" {
							t.Fatal("lost model-call identity")
						}
						if i%2 == 0 {
							if calls[call] || item.Payload["status"] != "receiving" || item.Payload["text"] != nil {
								t.Fatal("display mixed model calls")
							}
							calls[call] = true
						} else {
							text, _ := item.Payload["text"].(string)
							if item.Payload["status"] != "complete" || !strings.HasPrefix(text, "DISPLAY_ONLY_REASONING [REDACTED] [REDACTED]") || strings.Contains(text, "PRIVATE_KEY_BODY") || !utf8.ValidString(text) || len(text) > 16384 || reasoningEvents[i-1].Payload["model_call_id"] != call {
								t.Fatalf("unsafe or mixed display: %#v", item)
							}
							if i == 1 && item.Payload["truncated"] != true {
								t.Fatal("display limit did not retain its truncation marker")
							}
						}
					}
				} else if len(reasoningEvents) != 0 || len(liveReasoning) != 0 {
					t.Fatal("display enabled implicitly")
				}
				ledger, _, err := storage.GetRunUsageLedger(runID)
				if err != nil || ledger.Totals.ModelCalls != int(physical.Load()) || ledger.Totals.ToolCalls != 2 || ledger.Totals.OpenReservations != 0 {
					t.Fatalf("ledger=%#v HTTP=%d err=%v", ledger.Totals, physical.Load(), err)
				}
				items, _ := storage.ListRunEvents(runID)
				encoded, _ := json.Marshal(items)
				if strings.Contains(string(encoded), "sk-fixtureCredential123456") || strings.Contains(string(encoded), "PRIVATE_KEY_BODY") {
					t.Fatal("unsanitized reasoning persisted")
				}
				var durableReasoning []domain.RunEvent
				for _, item := range items {
					if item.Type == domain.EventModelReasoningDelta {
						t.Fatal("live reasoning batch persisted")
					}
					if item.Type == domain.EventModelReasoning {
						durableReasoning = append(durableReasoning, item)
					}
				}
				if len(durableReasoning) != len(reasoningEvents) {
					t.Fatalf("durable reasoning=%d streamed=%d", len(durableReasoning), len(reasoningEvents))
				}
				if display && !strings.Contains(string(encoded), "DISPLAY_ONLY_REASONING") {
					t.Fatal("sanitized reasoning was not saved")
				}
				if err := eventpkg.ValidateLifecycle(items); err != nil {
					t.Fatal(err)
				}
				records, _ := storage.ListModelRequestRecords(runID)
				if len(records) != int(physical.Load()) {
					t.Fatalf("capture=%d HTTP=%d", len(records), physical.Load())
				}
				if display {
					for _, record := range records {
						if record.Capture.Content != "" {
							t.Fatal("metadata-only capture retained reasoning")
						}
					}
				}
				t.Logf("mode=%s run=%s logical_calls=%d physical_attempts=%d Tool_calls=2 outcome=5 protocol=paired limitations=deterministic-provider", mode, runID, ledger.Totals.ModelCalls, physical.Load())
			})
		}
	}
}
