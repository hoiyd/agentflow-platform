package openai

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"agentflow-platform/apps/api/internal/budget"
	"agentflow-platform/apps/api/internal/domain"
	eventpkg "agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/tool"
)

func TestCompletionFinishReasons(t *testing.T) {
	tests := []struct {
		name, response, reason string
		kind                   ErrorKind
	}{
		{"stop", `{"choices":[{"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}]}`, "stop", ""},
		{"tool calls", `{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"call-1","type":"function","function":{"name":"calculator","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`, "tool_calls", ""},
		{"length", `{"choices":[{"message":{"role":"assistant","content":"partial"},"finish_reason":"length"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`, "length", ErrorIncompleteOutput},
		{"filter", `{"choices":[{"message":{"role":"assistant","content":"partial"},"finish_reason":"content_filter"}]}`, "content_filter", ErrorContentPolicy},
		{"refusal", `{"choices":[{"message":{"role":"assistant","refusal":"blocked"},"finish_reason":"stop"}]}`, "stop", ErrorContentPolicy},
		{"unknown", `{"choices":[{"message":{"role":"assistant","content":"partial"},"finish_reason":"future_reason"}]}`, "unknown", ErrorInvalidResponse},
		{"missing", `{"choices":[{"message":{"role":"assistant","content":"legacy"}}]}`, "missing", ""},
		{"missing tool finish", `{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"call-1","type":"function","function":{"name":"calculator","arguments":"{}"}}]}}]}`, "missing", ErrorInvalidResponse},
		{"inconsistent tool calls", `{"choices":[{"message":{"role":"assistant","content":"done"},"finish_reason":"tool_calls"}]}`, "tool_calls", ErrorInvalidResponse},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := retryTestClient()
			recorder := &attemptRecorderStub{}
			client.SetRequestRecorder(recorder)
			controller := &modelBudgetController{}
			calls := 0
			client.httpClient = &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
				calls++
				return modelHTTPResponse(200, tt.response), nil
			})}
			ctx := budget.WithController(context.Background(), controller)
			response, err := client.complete(ctx, map[string]any{"model": "test-model", "messages": []Message{{Role: "user", Content: "hello"}}})
			if calls != 1 || len(recorder.outcomes) != 1 || recorder.outcomes[0].FinishReason != tt.reason || controller.settles != 1 {
				t.Fatalf("calls=%d outcomes=%#v budget=%#v err=%v", calls, recorder.outcomes, controller, err)
			}
			if tt.kind == "" {
				if err != nil || recorder.outcomes[0].Status != "completed" {
					t.Fatalf("unexpected completion: %#v %v", response, err)
				}
				return
			}
			modelErr, ok := AsModelError(err)
			if !ok || modelErr.Kind != tt.kind || modelErr.Retryable || recorder.outcomes[0].Status != "failed" || len(response.Choices) != 1 {
				t.Fatalf("unexpected failed outcome: %#v %v", response, err)
			}
			if tt.reason == "length" && (response.Choices[0].Message.Content != "partial" || controller.usage.TotalTokens != 5) {
				t.Fatalf("partial output/usage lost: %#v %#v", response, controller.usage)
			}
		})
	}
}

func TestStreamFinishReasonAndSSEFrames(t *testing.T) {
	tests := []struct {
		name, stream, output, reason string
		kind                         ErrorKind
	}{
		{"stop multiline", ": keepalive\r\ndata: {\"choices\":[{\"delta\":{\"content\":\"hel\"}}]}\r\n\r\ndata: {\"choices\":[{\"delta\":{\"content\":\"lo\"},\r\ndata: \"finish_reason\":\"stop\"}]}\r\n\r\ndata:{\"choices\":[],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":2,\"total_tokens\":4}}\r\n\r\ndata: [DONE]\r\n\r\n", "hello", "stop", ""},
		{"length", "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"length\"}]}\n\ndata: {\"choices\":[],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":3,\"total_tokens\":5}}\n\ndata: [DONE]\n\n", "partial", "length", ErrorIncompleteOutput},
		{"filtered", "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"content_filter\"}]}\n\ndata: [DONE]\n\n", "", "content_filter", ErrorContentPolicy},
		{"refused", "data: {\"choices\":[{\"delta\":{\"refusal\":\"blocked\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n", "", "stop", ErrorContentPolicy},
		{"unknown", "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"other\"}]}\n\ndata: [DONE]\n\n", "", "unknown", ErrorInvalidResponse},
		{"missing", "data: {\"choices\":[{\"delta\":{\"content\":\"legacy\"}}]}\n\ndata: [DONE]\n\n", "legacy", "missing", ""},
		{"no done", "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n", "partial", "", ErrorInvalidResponse},
		{"truncated frame", "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\ndata: {\"choices\":[", "partial", "", ErrorInvalidResponse},
		{"content after stop", "data: {\"choices\":[{\"delta\":{\"content\":\"first\"}}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: {\"choices\":[{\"delta\":{\"content\":\"late\"}}]}\n\ndata: [DONE]\n\n", "first", "stop", ErrorInvalidResponse},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := retryTestClient()
			recorder := &attemptRecorderStub{}
			client.SetRequestRecorder(recorder)
			controller := &modelBudgetController{}
			calls := 0
			client.httpClient = &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
				calls++
				return modelHTTPResponse(200, tt.stream), nil
			})}
			events := make(chan StreamEvent, 8)
			ctx := budget.WithController(context.Background(), controller)
			emitted, output, usage, err := client.streamMessages(ctx, []Message{{Role: "user", Content: "hello"}}, events)
			if calls != 1 || len(recorder.outcomes) != 1 || recorder.outcomes[0].FinishReason != tt.reason || output != tt.output || emitted != (tt.output != "") {
				t.Fatalf("calls=%d outcomes=%#v emitted=%t output=%q usage=%#v err=%v", calls, recorder.outcomes, emitted, output, usage, err)
			}
			if tt.kind == "" {
				if err != nil || controller.settles != 1 {
					t.Fatalf("stream failed: %v budget=%#v", err, controller)
				}
				return
			}
			modelErr, ok := AsModelError(err)
			if !ok || modelErr.Kind != tt.kind || modelErr.Retryable || recorder.outcomes[0].Status != "failed" {
				t.Fatalf("wrong failure: %v outcome=%#v", err, recorder.outcomes)
			}
			wantSettles := 0
			if tt.reason != "" && tt.name != "content after stop" {
				wantSettles = 1
			}
			if controller.settles != wantSettles {
				t.Fatalf("usage settlement=%d, want %d", controller.settles, wantSettles)
			}
			if tt.reason == "length" && usage.TotalTokens != 5 {
				t.Fatalf("terminal usage lost: %#v", usage)
			}
		})
	}
}

func TestTruncatedToolCallNeverExecutes(t *testing.T) {
	client := retryTestClient()
	calls := 0
	client.httpClient = &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return modelHTTPResponse(200, `{"choices":[{"message":{"role":"assistant","content":"partial thought","tool_calls":[{"id":"call-1","type":"function","function":{"name":"calculator","arguments":"{\"expression\":"}}]},"finish_reason":"length"}]}`), nil
	})}
	store := &recordingEventStore{}
	events, errs := streamToolLoopForTest(client, context.Background(), "Use tools.", nil, "calculate 2+3", tool.DefaultCatalog(), eventpkg.NewRecorder(store), "run-1", "stage-1", nil, nil)
	for range events {
	}
	modelErr, ok := AsModelError(<-errs)
	if !ok || modelErr.Kind != ErrorIncompleteOutput || calls != 1 {
		t.Fatalf("truncated decision was retried or accepted: calls=%d err=%v", calls, modelErr)
	}
	items := store.items()
	if countEvents(items, domain.EventToolStarted) != 0 || countEvents(items, domain.EventModelCompleted) != 0 {
		t.Fatalf("truncated decision executed: %#v", items)
	}
	foundPartial := false
	for _, item := range items {
		if item.Type == domain.EventModelFailed && item.Payload["partial_output_preview"] == "partial thought" {
			foundPartial = true
		}
	}
	if !foundPartial {
		t.Fatalf("partial text not retained for diagnosis: %#v", items)
	}
}

func TestTruncatedTextCompletionRetainsPartialResponse(t *testing.T) {
	client := retryTestClient()
	client.httpClient = &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return modelHTTPResponse(200, `{"choices":[{"message":{"role":"assistant","content":"partial"},"finish_reason":"length"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`), nil
	})}
	completion, err := client.CompleteTextDetailed(context.Background(), "system", "hello")
	modelErr, ok := AsModelError(err)
	if !ok || modelErr.Kind != ErrorIncompleteOutput || completion.Text != "partial" || completion.Usage.TotalTokens != 5 {
		t.Fatalf("truncated text lost: completion=%#v err=%v", completion, err)
	}
}

func TestStreamEventSizeLimitClosesBody(t *testing.T) {
	client := retryTestClient()
	closed := false
	client.httpClient = &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		response := modelHTTPResponse(200, "data: "+strings.Repeat("x", 1024*1024+1)+"\n\n")
		response.Body = &closeTracker{ReadCloser: response.Body, closed: &closed}
		return response, nil
	})}
	_, _, _, err := client.streamMessagesWithUsageOption(context.Background(), []Message{{Role: "user", Content: "hi"}}, make(chan StreamEvent, 1), false)
	modelErr, ok := AsModelError(err)
	if !ok || modelErr.Kind != ErrorInvalidResponse || !closed {
		t.Fatalf("oversized frame not rejected/released: err=%v closed=%t", err, closed)
	}
}

func TestStreamDisconnectAfterDeltaReleasesPermitWithoutRetry(t *testing.T) {
	client := retryTestClient()
	calls, releases := 0, 0
	client.SetRequestLimiter(requestLimiterFunc(func(context.Context, string, int) (func(), error) {
		return func() { releases++ }, nil
	}))
	client.httpClient = &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		calls++
		response := modelHTTPResponse(200, "")
		response.Body = io.NopCloser(io.MultiReader(strings.NewReader("data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n"), disconnectReader{}))
		return response, nil
	})}
	emitted, output, _, err := client.streamMessagesWithUsageOption(context.Background(), []Message{{Role: "user", Content: "hi"}}, make(chan StreamEvent, 1), false)
	modelErr, ok := AsModelError(err)
	if !ok || modelErr.Kind != ErrorInvalidResponse || modelErr.Retryable || !emitted || output != "partial" || calls != 1 || releases != 1 {
		t.Fatalf("disconnect retried or leaked permit: calls=%d releases=%d emitted=%t output=%q err=%v", calls, releases, emitted, output, err)
	}
}

func TestStreamSSEAcrossSingleByteReads(t *testing.T) {
	client := retryTestClient()
	client.httpClient = &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		response := modelHTTPResponse(200, "")
		response.Body = io.NopCloser(&oneByteReader{reader: strings.NewReader("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")})
		return response, nil
	})}
	emitted, output, _, err := client.streamMessagesWithUsageOption(context.Background(), []Message{{Role: "user", Content: "hi"}}, make(chan StreamEvent, 1), false)
	if err != nil || !emitted || output != "ok" {
		t.Fatalf("fragmented stream: emitted=%t output=%q err=%v", emitted, output, err)
	}
}

type oneByteReader struct{ reader *strings.Reader }

func (r *oneByteReader) Read(p []byte) (int, error) { return r.reader.Read(p[:min(len(p), 1)]) }

type disconnectReader struct{}

func (disconnectReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

type closeTracker struct {
	io.ReadCloser
	closed *bool
}

func (c *closeTracker) Close() error {
	*c.closed = true
	return c.ReadCloser.Close()
}
