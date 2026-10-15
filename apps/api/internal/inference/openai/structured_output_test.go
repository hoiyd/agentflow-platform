package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"agentflow-platform/apps/api/internal/inference/provider"
)

// Failure inventory: an undeclared wire format must reject before HTTP; local
// mode must omit response_format; missing finish/refusal/cutoff cannot succeed.
func TestStructuredOutputWireContract(t *testing.T) {
	for _, mode := range []string{"strict", "local"} {
		for _, streaming := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%v", mode, streaming), func(t *testing.T) {
				var calls atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
						w.WriteHeader(400)
						return
					}
					call := calls.Add(1)
					if mode == "strict" {
						format, ok := body["response_format"].(map[string]any)
						if !ok || format["type"] != "json_schema" {
							t.Errorf("missing response_format: %v", body)
							w.WriteHeader(400)
							return
						}
						definition := format["json_schema"].(map[string]any)
						if definition["strict"] != true || definition["schema"].(map[string]any)["type"] != "object" {
							t.Errorf("wrong strict schema: %v", definition)
						}
					} else if _, ok := body["response_format"]; ok {
						t.Error("local mode claims provider constraint")
					}
					messages, _ := json.Marshal(body["messages"])
					if !strings.Contains(string(messages), "OUTPUT_CONTRACT") {
						t.Error("schema guidance absent")
					}
					seenNonSystem := false
					for _, item := range body["messages"].([]any) {
						message := item.(map[string]any)
						if message["role"] != "system" {
							seenNonSystem = true
						} else if seenNonSystem {
							t.Error("schema guidance split the conversation/Tool protocol")
						}
					}
					if streaming {
						if call == 1 {
							fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"calc\",\"type\":\"function\",\"function\":{\"name\":\"calculator\",\"arguments\":\"{\\\"expression\\\":\\\"2+3\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n")
							return
						}
						fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"{\\\"ok\\\":true}\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
					} else {
						fmt.Fprint(w, `{"choices":[{"message":{"content":"{\"ok\":true}"},"finish_reason":"stop"}]}`)
					}
				}))
				defer server.Close()
				client := NewClient("fixture", server.URL, "fixture")
				client.structuredOutputFormat = provider.OutputFormatOpenAIJSONSchema
				ctx := provider.WithStructuredOutput(context.Background(), provider.StructuredOutput{Mode: mode, ContractHash: "sha256:fixture", Version: "json-schema-output-v1", Schema: map[string]any{"type": "object", "properties": map[string]any{"ok": map[string]any{"type": "boolean"}}, "required": []any{"ok"}, "additionalProperties": false}})
				if streaming {
					definitions := []map[string]any{{"type": "function", "function": map[string]any{"name": "calculator", "parameters": map[string]any{"type": "object", "properties": map[string]any{"expression": map[string]any{"type": "string"}}}}}}
					prepared, err := client.PrepareAgentChat(ctx, provider.ChatRequest{SystemPrompt: "extract", Latest: "input", Definitions: definitions})
					if err != nil {
						t.Fatal(err)
					}
					events := make(chan StreamEvent, 10)
					choice, err := client.StreamToolRound(ctx, prepared, definitions, provider.ChatTrace{}, events)
					if err != nil {
						t.Fatal(err)
					}
					if len(choice.ToolCalls) != 1 {
						t.Fatalf("missing Tool round: %+v", choice)
					}
					messages := append(prepared.RawMessages, provider.Message{Role: "assistant", ToolCalls: choice.ToolCalls}, provider.Message{Role: "tool", ToolCallID: "calc", Content: `{"value":5}`})
					followup, err := client.PrepareFollowup(ctx, messages, definitions)
					if err != nil {
						t.Fatal(err)
					}
					if _, err = client.StreamToolRound(ctx, followup, definitions, provider.ChatTrace{}, events); err != nil {
						t.Fatal(err)
					}
				} else if _, err := client.CompleteTextDetailed(ctx, "extract", "input"); err != nil {
					t.Fatal(err)
				}
				want := int32(1)
				if streaming {
					want = 2
				}
				if calls.Load() != want {
					t.Fatalf("calls=%d", calls.Load())
				}
			})
		}
	}
}
