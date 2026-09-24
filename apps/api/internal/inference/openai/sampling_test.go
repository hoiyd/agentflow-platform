package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/tool"
)

func TestSamplingProfilesReachAnswerAndToolRequests(t *testing.T) {
	base := retryTestClient()
	policy := domain.DefaultGenerationPolicy()
	zero, topP, seed := 0.0, 0.75, int64(42)
	policy.AnswerStream.Temperature, policy.AnswerStream.TopP, policy.AnswerStream.Seed = &zero, &topP, &seed
	identity := base.RuntimeIdentity()
	identity.GenerationPolicy, identity.SeedSupported = policy, true
	client := base.WithRuntimeIdentity(identity).(*Client)
	var requests []map[string]any
	client.httpClient = &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		requests = append(requests, body)
		if body["stream"] == true {
			return modelHTTPResponse(200, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"), nil
		}
		return modelHTTPResponse(200, `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`), nil
	})}
	if _, err := client.CompleteTextDetailed(context.Background(), "system", "hello"); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := client.streamMessages(context.Background(), []Message{{Role: "user", Content: "hello"}}, make(chan StreamEvent, 2)); err != nil {
		t.Fatal(err)
	}
	events, errs := client.StreamAgentChatWithToolsTrace(context.Background(), "Use tools when needed.", nil, "hello", tool.DefaultCatalog(), nil, "", "", nil, nil)
	for range events {
	}
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
	if len(requests) != 3 {
		t.Fatalf("expected text, stream, and tool decision requests, got %d", len(requests))
	}
	if request := requests[0]; request["temperature"] != 0.2 || request["top_p"] != nil || request["seed"] != nil {
		t.Fatalf("nonstream completion used stream sampling: %#v", request)
	}
	if request := requests[1]; request["temperature"] != float64(0) || request["top_p"] != 0.75 || request["seed"] != float64(42) {
		t.Fatalf("stream ignored frozen profile: %#v", request)
	}
	decision := requests[2]
	if decision["temperature"] != 0.2 || decision["top_p"] != nil || decision["seed"] != nil || decision["tools"] == nil {
		t.Fatalf("tool decision used answer profile: %#v", decision)
	}
}

func TestInvalidSamplingFailsBeforeTransport(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*domain.GenerationPolicy)
		seedOK bool
	}{
		{"top_p", func(policy *domain.GenerationPolicy) { invalid := 2.0; policy.Completion.TopP = &invalid }, true},
		{"unsupported seed", func(policy *domain.GenerationPolicy) { seed := int64(42); policy.Completion.Seed = &seed }, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			base := retryTestClient()
			policy := domain.DefaultGenerationPolicy()
			test.mutate(&policy)
			identity := base.RuntimeIdentity()
			identity.GenerationPolicy, identity.SeedSupported = policy, test.seedOK
			client := base.WithRuntimeIdentity(identity).(*Client)
			calls := 0
			client.httpClient = &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
				calls++
				return modelHTTPResponse(200, `{"choices":[{"message":{"role":"assistant","content":"unexpected"}}]}`), nil
			})}
			_, err := client.CompleteTextDetailed(context.Background(), "system", "hello")
			modelErr, ok := AsModelError(err)
			if !ok || modelErr.Kind != ErrorInvalidRequest || calls != 0 {
				t.Fatalf("invalid sampling crossed transport: calls=%d err=%v", calls, err)
			}
		})
	}
}
