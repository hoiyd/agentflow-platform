package openai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/budget"
	"agentflow-platform/apps/api/internal/contextassembly"
	"agentflow-platform/apps/api/internal/domain"
	eventpkg "agentflow-platform/apps/api/internal/event"
)

func TestBudgetWrapsPhysicalRetriesAndCapsCompletion(t *testing.T) {
	client := retryTestClient()
	controller := &modelBudgetController{maxCompletionTokens: 7}
	attempts := 0
	permits := 0
	client.SetRequestLimiter(requestLimiterFunc(func(context.Context, string, int) (func(), error) {
		permits++
		return func() {}, nil
	}))
	client.httpClient = &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		attempts++
		body, _ := io.ReadAll(request.Body)
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if payload["max_tokens"] != float64(7) {
			t.Fatalf("expected budget output cap, payload=%#v", payload)
		}
		if attempts == 1 {
			return modelHTTPResponse(503, `{"error":{"message":"temporarily unavailable"}}`), nil
		}
		return modelHTTPResponse(200, `{"choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`), nil
	})}
	ctx := budget.WithController(context.Background(), controller)
	completion, err := client.CompleteTextDetailed(ctx, "system", "hello")
	if err != nil || completion.Text != "ok" || attempts != 2 || permits != 2 || controller.begins != 1 || controller.settles != 1 {
		t.Fatalf("logical/physical controls crossed boundaries: completion=%#v attempts=%d permits=%d controller=%#v err=%v", completion, attempts, permits, controller, err)
	}
	if controller.estimate.OperationID == "" || controller.usage.TotalTokens != 5 {
		t.Fatalf("missing stable operation or settlement usage: %#v", controller)
	}
}

func TestCompleteRetriesProviderUnavailable(t *testing.T) {
	client := retryTestClient()
	attempts := 0
	client.httpClient = &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		attempts++
		if attempts == 1 {
			return modelHTTPResponse(503, `{"error":{"message":"temporarily unavailable"}}`), nil
		}
		return modelHTTPResponse(200, `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`), nil
	})}

	completion, err := client.CompleteTextDetailed(context.Background(), "system", "hello")
	if err != nil || completion.Text != "ok" || attempts != 2 {
		t.Fatalf("expected retried completion, text=%q attempts=%d err=%v", completion.Text, attempts, err)
	}
}

func TestPhysicalRetryReusesOneContextManifest(t *testing.T) {
	client := retryTestClient()
	attempts := 0
	manifests := 0
	client.httpClient = &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		attempts++
		if attempts == 1 {
			return modelHTTPResponse(503, `{"error":{"message":"temporarily unavailable"}}`), nil
		}
		return modelHTTPResponse(200, `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`), nil
	})}
	ctx := eventpkg.WithScope(context.Background(), eventpkg.Scope{RunID: "run-1", TurnID: "turn-1"})
	ctx = contextassembly.WithSession(ctx, contextassembly.Session{
		Config: contextassembly.DefaultConfig(),
		Sink: eventpkg.SinkFunc(func(_ context.Context, item domain.RunEvent) error {
			if item.Type == domain.EventContextAssembled {
				manifests++
			}
			return nil
		}),
	})

	completion, err := client.CompleteTextDetailed(ctx, "system", "hello")
	if err != nil || completion.Text != "ok" || attempts != 2 || manifests != 1 {
		t.Fatalf("expected two attempts and one manifest, text=%q attempts=%d manifests=%d err=%v", completion.Text, attempts, manifests, err)
	}
}

func TestRetryUsesIndependentRequestPermitPerAttempt(t *testing.T) {
	client := retryTestClient()
	events := make([]string, 0, 6)
	client.SetRequestLimiter(requestLimiterFunc(func(context.Context, string, int) (func(), error) {
		events = append(events, "acquire")
		return func() { events = append(events, "release") }, nil
	}))
	attempts := 0
	client.httpClient = &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		attempts++
		events = append(events, "request")
		if attempts == 1 {
			return modelHTTPResponse(503, `{"error":{"message":"temporarily unavailable"}}`), nil
		}
		return modelHTTPResponse(200, `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`), nil
	})}

	if _, err := client.CompleteTextDetailed(context.Background(), "system", "hello"); err != nil {
		t.Fatalf("complete: %v", err)
	}
	want := []string{"acquire", "request", "release", "acquire", "request", "release"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("request permits crossed retry boundary: got %v want %v", events, want)
	}
}

func TestCompleteDoesNotRetryAuthenticationError(t *testing.T) {
	client := retryTestClient()
	attempts := 0
	client.httpClient = &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		attempts++
		return modelHTTPResponse(401, `{"error":{"message":"Invalid API key","code":"invalid_api_key"}}`), nil
	})}

	_, err := client.CompleteTextDetailed(context.Background(), "system", "hello")
	modelErr, ok := AsModelError(err)
	if !ok || modelErr.Kind != ErrorAuthentication || attempts != 1 {
		t.Fatalf("expected terminal authentication error, attempts=%d err=%#v", attempts, modelErr)
	}
}

func TestStreamDoesNotRetryAfterOutputDelta(t *testing.T) {
	client := retryTestClient()
	attempts := 0
	client.httpClient = &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		attempts++
		return modelHTTPResponse(200, "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n"), nil
	})}

	events := make(chan StreamEvent, 4)
	emitted, output, _, err := client.streamMessagesWithUsageOption(context.Background(), []Message{{Role: "user", Content: "hello"}}, events, false)
	modelErr, ok := AsModelError(err)
	if !ok || modelErr.Kind != ErrorInvalidResponse || modelErr.Retryable || !emitted || output != "partial" || attempts != 1 {
		t.Fatalf("expected non-retried partial stream, emitted=%t output=%q attempts=%d err=%#v", emitted, output, attempts, modelErr)
	}
}

func TestStreamUsageFallbackOnlyForUnsupportedCapability(t *testing.T) {
	client := retryTestClient()
	controller := &modelBudgetController{maxCompletionTokens: 9}
	attempts := 0
	client.httpClient = &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		attempts++
		body, _ := io.ReadAll(request.Body)
		if attempts == 1 {
			if !strings.Contains(string(body), "stream_options") {
				t.Fatal("expected initial stream usage request")
			}
			return modelHTTPResponse(400, `{"error":{"message":"stream_options.include_usage is not supported","code":"invalid_request_error"}}`), nil
		}
		if strings.Contains(string(body), "stream_options") {
			t.Fatal("expected capability fallback without stream_options")
		}
		return modelHTTPResponse(200, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n"), nil
	})}

	events := make(chan StreamEvent, 4)
	ctx := budget.WithController(context.Background(), controller)
	emitted, output, _, err := client.streamMessages(ctx, []Message{{Role: "user", Content: "hello"}}, events)
	if err != nil || !emitted || output != "ok" || attempts != 2 || controller.begins != 1 || controller.settles != 1 {
		t.Fatalf("expected one budgeted stream across capability fallback, emitted=%t output=%q attempts=%d controller=%#v err=%v", emitted, output, attempts, controller, err)
	}
}

type modelBudgetController struct {
	maxCompletionTokens int
	begins              int
	settles             int
	estimate            budget.ModelCallEstimate
	estimates           []budget.ModelCallEstimate
	usage               budget.ModelUsage
}

func (c *modelBudgetController) BeginModelCall(_ context.Context, estimate budget.ModelCallEstimate) (budget.ModelReservation, error) {
	c.begins++
	c.estimate = estimate
	c.estimates = append(c.estimates, estimate)
	return budget.ModelReservation{
		OperationID: estimate.OperationID, Purpose: estimate.Purpose, Model: estimate.Model,
		EstimatedPromptTokens: estimate.EstimatedPromptTokens, MaxCompletionTokens: c.maxCompletionTokens,
	}, nil
}

func (c *modelBudgetController) SettleModelCall(_ context.Context, _ budget.ModelReservation, usage budget.ModelUsage) error {
	c.settles++
	c.usage = usage
	return nil
}

func (c *modelBudgetController) RecordToolCall(context.Context, budget.ToolCall) error { return nil }

func retryTestClient() *Client {
	client := NewClient("test-key", "https://provider.example/v1", "test-model")
	client.SetRetryPolicy(RetryPolicy{MaxAttempts: 3, BaseDelay: time.Millisecond, MaxDelay: time.Millisecond})
	return client
}

type requestLimiterFunc func(context.Context, string, int) (func(), error)

func (f requestLimiterFunc) AcquireRequest(ctx context.Context, apiKey string, estimatedTokens int) (func(), error) {
	return f(ctx, apiKey, estimatedTokens)
}

func modelHTTPResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}
