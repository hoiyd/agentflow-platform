package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/budget"
	"agentflow-platform/apps/api/internal/inference/provider"
)

func TestReasoningDisplayWirePrivacyAndFailureBoundaries(t *testing.T) {
	secret := "sk-fixtureCredential123456"
	reasoning := "Inspect evidence using " + secret + ". Then explain."
	frame := func(value string) string {
		encoded, _ := json.Marshal(map[string]string{"reasoning_content": value})
		return toolStreamFrame(string(encoded), "")
	}
	base := frame("Inspect evidence using sk-fixture") + toolStreamFrame(`{"content":"Answer"}`, "") + frame("Credential123456. Then explain.")
	for _, test := range []struct {
		name, format, wire, wantStatus string
		wantErr, truncated             bool
	}{
		{"interleaved", "deepseek_reasoning_content", base + toolStreamFrame(`{}`, "stop") + "data: [DONE]\n\n", "complete", false, false},
		{"disabled", "", base + toolStreamFrame(`{}`, "stop") + "data: [DONE]\n\n", "", false, false},
		{"empty", "deepseek_reasoning_content", frame("") + toolStreamFrame(`{"content":"Answer"}`, "stop") + "data: [DONE]\n\n", "", false, false},
		{"missing", "deepseek_reasoning_content", toolStreamFrame(`{"content":"Answer"}`, "stop") + "data: [DONE]\n\n", "", false, false},
		{"disconnect", "deepseek_reasoning_content", frame(reasoning), "interrupted", true, false},
		{"malformed", "deepseek_reasoning_content", frame(reasoning) + "data: broken\n\n", "interrupted", true, false},
		{"length", "deepseek_reasoning_content", base + toolStreamFrame(`{}`, "length") + "data: [DONE]\n\n", "interrupted", true, false},
		{"missing finish", "deepseek_reasoning_content", base + "data: [DONE]\n\n", "interrupted", true, false},
		{"reasoning only", "deepseek_reasoning_content", frame(reasoning) + toolStreamFrame(`{}`, "stop") + "data: [DONE]\n\n", "interrupted", true, false},
		{"display cap", "deepseek_reasoning_content", frame(strings.Repeat("z", 20000)) + toolStreamFrame(`{"content":"Answer"}`, "stop") + "data: [DONE]\n\n", "complete", false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := retryTestClient()
			identity := client.RuntimeIdentity()
			identity.ReasoningDisplayFormat = test.format
			client = client.WithRuntimeIdentity(identity).(*Client)
			client.SetRetryPolicy(RetryPolicy{MaxAttempts: 3, BaseDelay: time.Millisecond, MaxDelay: time.Millisecond})
			attempts := 0
			client.httpClient = &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
				attempts++
				return modelHTTPResponse(200, test.wire), nil
			})}
			events := make(chan provider.StreamEvent, 16)
			ctx := budget.WithOperation(context.Background(), "call-fixture")
			result, err := client.streamChat(ctx, []Message{{Role: "user", Content: "Explain"}}, nil, events)
			close(events)
			if (err != nil) != test.wantErr || attempts != 1 {
				t.Fatalf("attempts=%d err=%v", attempts, err)
			}
			if test.name == "disabled" && result.usage.CompletionTokens != estimateUsage("", result.output+reasoning).CompletionTokens {
				t.Fatal("display opt-in changed private-output usage accounting")
			}
			var seen []*provider.ReasoningDisplay
			for item := range events {
				if item.Reasoning != nil {
					seen = append(seen, item.Reasoning)
				}
			}
			if test.wantStatus == "" {
				if len(seen) != 0 {
					t.Fatalf("unexpected display: %#v", seen)
				}
				return
			}
			if len(seen) != 2 || seen[0].Status != "receiving" || seen[0].Text != "" || seen[1].Status != test.wantStatus {
				t.Fatalf("display=%#v", seen)
			}
			last := seen[1]
			if last.ModelCallID == "" || last.Format != test.format || last.Truncated != test.truncated || len(last.Text) > 16384 || strings.Contains(last.Text, secret) {
				t.Fatalf("privacy/identity boundary: %#v", last)
			}
			if test.wantErr && last.Text != "" {
				t.Fatal("partial reasoning leaked")
			}
			if !test.wantErr && !test.truncated && !strings.Contains(last.Text, "[REDACTED]") {
				t.Fatal("split credential not redacted")
			}
			if test.name == "interleaved" && (result.output != "Answer" || result.choice.ReasoningContent == nil || *result.choice.ReasoningContent != reasoning) {
				t.Fatal("display mutated protocol or answer")
			}
		})
	}
}
