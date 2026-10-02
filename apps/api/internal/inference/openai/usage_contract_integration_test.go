package openai

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"agentflow-platform/apps/api/internal/budget"
	"agentflow-platform/apps/api/internal/testsupport/modelstream"
)

// Adapter wire integration. Browser fixtures cover durable Replay; these cases
// isolate physical retries/fallback and EOF, which must never double-settle.
func TestUsageWireAttemptsAndLogicalSettlement(t *testing.T) {
	completion := `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":4,"total_tokens":14,"prompt_cache_hit_tokens":6,"prompt_cache_miss_tokens":4,"completion_tokens_details":{"reasoning_tokens":2}}}`
	for _, scenario := range []string{"completion", "stream", "retry", "stream fallback", "missing done", "missing usage", "failure"} {
		t.Run(scenario, func(t *testing.T) {
			physical := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				physical++
				if scenario == "failure" || (scenario == "retry" && physical == 1) {
					w.WriteHeader(503)
					fmt.Fprint(w, `{"error":{"message":"unavailable"}}`)
					return
				}
				if scenario == "stream fallback" && physical == 1 {
					w.WriteHeader(400)
					fmt.Fprint(w, `{"error":{"message":"stream_options include_usage is not supported"}}`)
					return
				}
				response := completion
				if scenario == "missing usage" {
					response = `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`
				}
				if strings.Contains(scenario, "stream") || scenario == "missing done" {
					wire, err := modelstream.Completion(response)
					if err != nil {
						t.Fatal(err)
					}
					if scenario == "missing done" {
						wire = strings.ReplaceAll(wire, "data: [DONE]\n\n", "")
					}
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprint(w, wire)
				} else {
					fmt.Fprint(w, response)
				}
			}))
			defer server.Close()
			client := retryTestClient()
			client.baseURL = server.URL
			client.routeID = "fixture"
			controller, recorder := &modelBudgetController{}, &attemptRecorderStub{}
			client.SetRequestRecorder(recorder)
			ctx := budget.WithController(context.Background(), controller)
			if strings.Contains(scenario, "stream") || scenario == "missing done" {
				_, err := client.streamChat(ctx, []Message{{Role: "user", Content: "hello"}}, nil, make(chan StreamEvent, 10))
				if (err != nil) != (scenario == "missing done") {
					t.Fatalf("stream error: %v", err)
				}
			} else {
				_, err := client.complete(ctx, map[string]any{"model": "test-model", "messages": []Message{{Role: "user", Content: "hello"}}})
				if (err != nil) != (scenario == "failure") {
					t.Fatalf("completion error: %v", err)
				}
			}
			expectedSettlements := 1
			if scenario == "failure" || scenario == "missing done" {
				expectedSettlements = 0
			}
			if controller.begins != 1 || controller.settles != expectedSettlements || controller.estimate.RouteID != "fixture" {
				t.Fatalf("logical accounting changed: %+v", controller)
			}
			if len(recorder.outcomes) != physical {
				t.Fatalf("lost physical attempt: %d/%d", len(recorder.outcomes), physical)
			}
			outcome := recorder.outcomes[len(recorder.outcomes)-1]
			if scenario == "failure" || scenario == "missing usage" {
				if outcome.Breakdown != nil || (scenario == "failure" && outcome.UsageAvailable) || (scenario == "missing usage" && !outcome.UsageEstimated) {
					t.Fatalf("invented attempt usage: %+v", outcome)
				}
				if scenario == "missing usage" && (!controller.usage.Estimated || controller.usage.Breakdown != nil) {
					t.Fatalf("fallback invented subsets: %+v", controller.usage)
				}
				return
			}
			if outcome.Breakdown == nil || *outcome.Breakdown.CachedInputTokens != 6 || *outcome.Breakdown.ReasoningTokens != 2 {
				t.Fatalf("attempt detail lost: %+v", outcome)
			}
			if expectedSettlements == 1 && (controller.usage.TotalTokens != 14 || controller.usage.Breakdown == nil || *controller.usage.Breakdown.CachedInputTokens != 6) {
				t.Fatalf("settlement lost subsets: %+v", controller.usage)
			}
			for _, before := range recorder.outcomes[:len(recorder.outcomes)-1] {
				if before.UsageAvailable || before.Breakdown != nil {
					t.Fatalf("failed attempt invented usage: %+v", before)
				}
			}
		})
	}
}
