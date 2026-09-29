package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/budget"
	"agentflow-platform/apps/api/internal/inference/provider"
	"agentflow-platform/apps/api/internal/tool"
)

func toolStreamFrame(delta string, finish string) string {
	reason := "null"
	if finish != "" {
		reason = fmt.Sprintf("%q", finish)
	}
	return fmt.Sprintf("data: {\"choices\":[{\"index\":0,\"delta\":%s,\"finish_reason\":%s}]}\n\n", delta, reason)
}

func TestStreamingToolRoundProtocol(t *testing.T) {
	first := `{"tool_calls":[{"index":0,"id":"a","type":"function","function":{"name":"calculator","arguments":"{\"expression\":"}},{"index":1,"id":"b","type":"function","function":{"name":"calculator","arguments":"{\"expression\":"}}]}`
	last := `{"tool_calls":[{"index":1,"function":{"arguments":"\"2 + 2\"}"}},{"index":0,"function":{"arguments":"\"1 + 1\"}"}}]}`
	for _, test := range []struct {
		name, body string
		kind       ErrorKind
		calls      int
		output     string
	}{
		{"interleaved calls", toolStreamFrame(first, "") + toolStreamFrame(last, "tool_calls") + "data: [DONE]\n\n", "", 2, ""},
		{"missing index", toolStreamFrame(`{"tool_calls":[{"id":"a"}]}`, "tool_calls") + "data: [DONE]\n\n", ErrorInvalidResponse, 0, ""},
		{"negative index", toolStreamFrame(`{"tool_calls":[{"index":-1}]}`, "") + "data: [DONE]\n\n", ErrorInvalidResponse, 0, ""},
		{"oversized index", toolStreamFrame(`{"tool_calls":[{"index":1024}]}`, "") + "data: [DONE]\n\n", ErrorInvalidResponse, 0, ""},
		{"sparse index", toolStreamFrame(`{"tool_calls":[{"index":1,"id":"a","type":"function","function":{"name":"calculator","arguments":"{}"}}]}`, "tool_calls") + "data: [DONE]\n\n", ErrorInvalidResponse, 0, ""},
		{"changed identity", toolStreamFrame(first, "") + toolStreamFrame(`{"tool_calls":[{"index":0,"id":"changed"}]}`, "tool_calls") + "data: [DONE]\n\n", ErrorInvalidResponse, 0, ""},
		{"changed type", toolStreamFrame(first, "") + toolStreamFrame(`{"tool_calls":[{"index":0,"type":"other"}]}`, "tool_calls") + "data: [DONE]\n\n", ErrorInvalidResponse, 0, ""},
		{"missing identity", toolStreamFrame(`{"tool_calls":[{"index":0,"type":"function","function":{"name":"calculator","arguments":"{}"}}]}`, "tool_calls") + "data: [DONE]\n\n", ErrorInvalidResponse, 0, ""},
		{"missing finish", toolStreamFrame(first, "") + "data: [DONE]\n\n", ErrorInvalidResponse, 0, ""},
		{"wrong finish", toolStreamFrame(first, "stop") + "data: [DONE]\n\n", ErrorInvalidResponse, 0, ""},
		{"truncated calls", toolStreamFrame(first, "length") + "data: [DONE]\n\n", ErrorIncompleteOutput, 0, ""},
		{"post finish arguments", toolStreamFrame(first, "tool_calls") + toolStreamFrame(last, "") + "data: [DONE]\n\n", ErrorInvalidResponse, 0, ""},
		{"disconnect after content", toolStreamFrame(`{"content":"partial"}`, ""), ErrorInvalidResponse, 0, "partial"},
		{"refusal", toolStreamFrame(`{"refusal":"no"}`, "stop") + "data: [DONE]\n\n", ErrorContentPolicy, 0, ""},
		{"empty answer", toolStreamFrame(`{}`, "stop") + "data: [DONE]\n\n", ErrorInvalidResponse, 0, ""},
		{"unexpected choice", "data: {\"choices\":[{\"index\":1,\"delta\":{}}]}\n\n", ErrorInvalidResponse, 0, ""},
		{"multiple choices", "data: {\"choices\":[{\"index\":0,\"delta\":{}},{\"index\":1,\"delta\":{}}]}\n\n", ErrorInvalidResponse, 0, ""},
		{"bounded aggregate", strings.Repeat(toolStreamFrame(`{"reasoning_content":"`+strings.Repeat("x", 600000)+`"}`, ""), 2), ErrorInvalidResponse, 0, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := retryTestClient()
			client.SetRetryPolicy(RetryPolicy{MaxAttempts: 1})
			physical := 0
			client.httpClient = &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				physical++
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["stream"] != true || body["tools"] == nil || body["tool_choice"] != "auto" {
					t.Fatalf("invalid tool stream request: %#v err=%v", body, err)
				}
				return modelHTTPResponse(200, test.body), nil
			})}
			events := make(chan provider.StreamEvent, 8)
			choice, err := client.StreamToolRound(context.Background(), provider.PreparedChat{Messages: []Message{{Role: "user", Content: "calculate"}}}, tool.DefaultCatalog().Definitions(), provider.ChatTrace{}, events)
			close(events)
			var output strings.Builder
			for event := range events {
				if event.Reset {
					output.Reset()
				}
				output.WriteString(event.Delta)
			}
			if test.kind == "" {
				if err != nil || len(choice.ToolCalls) != test.calls || choice.ToolCalls[0].Function.Arguments != `{"expression":"1 + 1"}` || choice.ToolCalls[1].Function.Arguments != `{"expression":"2 + 2"}` {
					t.Fatalf("choice=%#v err=%v", choice, err)
				}
			} else if modelErr, ok := AsModelError(err); !ok || modelErr.Kind != test.kind || physical != 1 {
				t.Fatalf("physical=%d expected=%s err=%v", physical, test.kind, err)
			}
			if output.String() != test.output {
				t.Fatalf("output=%q want=%q", output.String(), test.output)
			}
		})
	}
}

func TestStreamingToolRoundRetryAndUsageBoundaries(t *testing.T) {
	for _, path := range []string{"success", "estimated usage", "pre-output retry", "stream usage unsupported", "disconnect after delta", "output limit"} {
		t.Run(path, func(t *testing.T) {
			client := retryTestClient()
			client.SetRetryPolicy(RetryPolicy{MaxAttempts: 3, BaseDelay: time.Millisecond, MaxDelay: time.Millisecond})
			controller := &modelBudgetController{maxCompletionTokens: 32}
			ctx := budget.WithController(t.Context(), controller)
			ctx = withOutputTokenLimit(ctx, 16)
			recorder := &attemptRecorderStub{}
			client.SetRequestRecorder(recorder)
			physical, releases := 0, 0
			var estimated Usage
			client.SetRequestLimiter(requestLimiterFunc(func(context.Context, string, int) (func(), error) { return func() { releases++ }, nil }))
			client.httpClient = &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				physical++
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["tools"] == nil || body["stream"] != true || body["temperature"] != 0.2 || body["max_tokens"] != float64(16) {
					t.Fatalf("contract changed: %#v err=%v", body, err)
				}
				payload, _ := json.Marshal(body)
				estimated = estimateUsage(string(payload), "partialprivate deliberation")
				if physical == 1 {
					switch path {
					case "pre-output retry":
						return modelHTTPResponse(503, `{"error":{"message":"unavailable"}}`), nil
					case "stream usage unsupported":
						return modelHTTPResponse(400, `{"error":{"message":"stream_options include_usage is not supported"}}`), nil
					}
				}
				if path == "stream usage unsupported" && physical == 2 && body["stream_options"] != nil {
					t.Fatal("fallback repeated unsupported option")
				}
				stream := toolStreamFrame(`{"content":"partial"}`, "")
				if path != "disconnect after delta" {
					reason := "stop"
					if path == "output limit" {
						reason = "length"
					}
					if path == "estimated usage" {
						stream += toolStreamFrame(`{"reasoning_content":"private deliberation"}`, reason) + "data: [DONE]\n\n"
					} else {
						stream += toolStreamFrame(`{}`, reason) + "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":7,\"completion_tokens\":3,\"total_tokens\":10}}\n\ndata: [DONE]\n\n"
					}
				}
				return modelHTTPResponse(200, stream), nil
			})}
			events := make(chan StreamEvent, 8)
			_, err := client.StreamToolRound(ctx, provider.PreparedChat{Messages: []Message{{Role: "user", Content: "answer"}}}, tool.DefaultCatalog().Definitions(), provider.ChatTrace{}, events)
			close(events)
			var output string
			for event := range events {
				output += event.Delta
			}
			wantAttempts, wantSettles := 1, 1
			if path == "pre-output retry" || path == "stream usage unsupported" {
				wantAttempts = 2
			}
			if path == "disconnect after delta" {
				wantSettles = 0
			}
			if physical != wantAttempts || releases != physical || len(recorder.outcomes) != physical || controller.begins != 1 || controller.settles != wantSettles || output != "partial" {
				t.Fatalf("path=%s attempts=%d releases=%d budget=%#v output=%q err=%v", path, physical, releases, controller, output, err)
			}
			last := recorder.outcomes[len(recorder.outcomes)-1]
			if last.TimeToFirstTokenMS == nil {
				t.Fatal("real stream lost first-token telemetry")
			}
			if path == "estimated usage" {
				if controller.usage.PromptTokens != estimated.PromptTokens || controller.usage.CompletionTokens != estimated.CompletionTokens || controller.usage.TotalTokens != estimated.TotalTokens || !controller.usage.Estimated {
					t.Fatalf("request schema or private continuation omitted from usage: got=%#v want=%#v", controller.usage, estimated)
				}
			} else if wantSettles == 1 && (controller.usage.TotalTokens != 10 || controller.usage.Estimated) {
				t.Fatalf("provider usage lost: %#v", controller.usage)
			}
			if path == "disconnect after delta" || path == "output limit" {
				kind := ErrorInvalidResponse
				if path == "output limit" {
					kind = ErrorIncompleteOutput
				}
				if modelErr, ok := AsModelError(err); !ok || modelErr.Kind != kind || modelErr.Retryable || last.Status != "failed" {
					t.Fatalf("failure became successful/retryable: %#v err=%v", last, err)
				}
			} else if err != nil || last.Status != "completed" {
				t.Fatalf("path=%s err=%v outcome=%#v", path, err, last)
			}
		})
	}
}

func TestStreamingToolRoundAssemblesReasoningAndNameFragments(t *testing.T) {
	for _, reasoning := range []string{"", "private continuation"} {
		t.Run(fmt.Sprintf("reasoning_bytes=%d", len(reasoning)), func(t *testing.T) {
			client := retryTestClient()
			controller := &modelBudgetController{}
			ctx := budget.WithController(t.Context(), controller)
			var estimated Usage
			first := fmt.Sprintf(`{"reasoning_content":%q,"tool_calls":[{"index":0,"id":"a","type":"function","function":{"name":"calcu","arguments":"{"}}]}`, reasoning[:len(reasoning)/2])
			second := fmt.Sprintf(`{"reasoning_content":%q,"tool_calls":[{"index":0,"id":"a","type":"function","function":{"name":"lator","arguments":"}"}}]}`, reasoning[len(reasoning)/2:])
			client.httpClient = &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				payload, _ := json.Marshal(body)
				estimated = estimateUsage(string(payload), reasoning+"calculator{}")
				return modelHTTPResponse(200, toolStreamFrame(first, "")+toolStreamFrame(second, "tool_calls")+"data: [DONE]\n\n"), nil
			})}
			events := make(chan StreamEvent, 8)
			choice, err := client.StreamToolRound(ctx, provider.PreparedChat{}, tool.DefaultCatalog().Definitions(), provider.ChatTrace{}, events)
			if err != nil || choice.ReasoningContent == nil || *choice.ReasoningContent != reasoning || len(choice.ToolCalls) != 1 || choice.ToolCalls[0].Function.Name != "calculator" || choice.ToolCalls[0].Function.Arguments != "{}" || len(events) != 0 {
				t.Fatalf("choice=%#v emitted=%d err=%v", choice, len(events), err)
			}
			if controller.usage.PromptTokens != estimated.PromptTokens || controller.usage.CompletionTokens != estimated.CompletionTokens || !controller.usage.Estimated {
				t.Fatalf("Tool protocol state omitted from usage: got=%#v want=%#v", controller.usage, estimated)
			}
		})
	}
}
