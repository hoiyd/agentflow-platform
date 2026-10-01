package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

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
		{"live display cap", "deepseek_reasoning_content", frame(strings.Repeat("z ", 10000)) + frame("more text ") + toolStreamFrame(`{"content":"Answer"}`, "stop") + "data: [DONE]\n\n", "complete", false, true},
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
			if len(seen) < 2 || seen[0].Status != "receiving" || seen[0].Text != "" || seen[len(seen)-1].Status != test.wantStatus {
				t.Fatalf("display=%#v", seen)
			}
			last := seen[len(seen)-1]
			for _, entry := range seen {
				if strings.Contains(entry.Text, secret) || len(entry.Text) > maxReasoningDisplayBytes || !utf8.ValidString(entry.Text) {
					t.Fatalf("unsafe live/final display: %#v", entry)
				}
			}
			if test.name == "live display cap" && (len(seen) != 3 || !seen[1].Truncated || seen[1].Status != "receiving") {
				t.Fatalf("live cap did not stop additional batches: %#v", seen)
			}
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

func TestReasoningDisplayPrefixWithholdsIncompleteCredentials(t *testing.T) {
	for _, test := range []struct{ name, raw, key, forbidden string }{
		{"configured", "Safe words. custom secret with spaces More evidence. ", "custom secret with spaces", "custom"},
		{"configured unicode", "Safe words. 私密 凭据\n更多文字 More evidence. ", "私密 凭据\n更多文字", "私密"},
		{"token", "Safe words. sk-fixtureCredential123456 More evidence. ", "", "sk-fixture"},
		{"bearer", "Safe words. Bearer abcdefghijklmnop More evidence. ", "", "abcdefghijklmnop"},
		{"assignment", "Safe words. api_key = abcdefghijklmnop More evidence. ", "", "abcdefghijklmnop"},
		{"multiline assignment", "Safe words. password=\nabcdefghijklmnop More evidence. ", "", "abcdefghijklmnop"},
		{"private key", "Safe words. -----BEGIN PRIVATE KEY-----\nPRIVATE_KEY_BODY\n-----END PRIVATE KEY----- More evidence. ", "", "PRIVATE_KEY_BODY"},
		{"url", "Safe words. postgres://user:abcdefghijklmnop@host More evidence. ", "", "abcdefghijklmnop"},
		{"unicode", "查看证据。\n继续解释。\n", "", "never-present"},
	} {
		t.Run(test.name, func(t *testing.T) {
			for end := 0; end <= len(test.raw); end++ {
				if !utf8.ValidString(test.raw[:end]) {
					continue
				}
				text, _ := reasoningDisplayPrefix(test.raw[:end], test.key)
				if !utf8.ValidString(text) || len(text) > maxReasoningDisplayBytes || strings.Contains(text, test.forbidden) {
					t.Fatalf("unsafe prefix at byte %d: %q", end, text)
				}
			}
		})
	}
	if text, _ := reasoningDisplayPrefix("unfinished-token", ""); text != "" {
		t.Fatalf("unfinished token released: %q", text)
	}
	if text, truncated := reasoningDisplayPrefix(strings.Repeat("解释 ", 5000), ""); !truncated || len(text) > maxReasoningDisplayBytes || !utf8.ValidString(text) {
		t.Fatalf("invalid capped prefix: bytes=%d truncated=%v", len(text), truncated)
	}
}

func TestReasoningDisplayPublishesSafeBatches(t *testing.T) {
	client := retryTestClient()
	identity := client.RuntimeIdentity()
	identity.ReasoningDisplayFormat = provider.ReasoningFormatDeepSeek
	client = client.WithRuntimeIdentity(identity).(*Client)
	client.apiKey = "configured secret across spaces"
	raw := "Inspect evidence. " + client.apiKey + " and sk-fixtureCredential123456. " + strings.Repeat("more evidence ", 30)
	encoded, _ := json.Marshal(map[string]string{"reasoning_content": raw})
	client.httpClient = &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return modelHTTPResponse(200, toolStreamFrame(string(encoded), "")+toolStreamFrame(`{"content":"Answer"}`, "stop")+"data: [DONE]\n\n"), nil
	})}
	events := make(chan provider.StreamEvent, 16)
	result, err := client.streamChat(budget.WithOperation(context.Background(), "call-fixture"), []Message{{Role: "user", Content: "Explain"}}, nil, events)
	close(events)
	if err != nil {
		t.Fatal(err)
	}
	live := false
	for item := range events {
		if item.Reasoning == nil {
			continue
		}
		if strings.Contains(item.Reasoning.Text, client.apiKey) || strings.Contains(item.Reasoning.Text, "sk-fixtureCredential") {
			t.Fatalf("credential in display: %#v", item.Reasoning)
		}
		if item.Reasoning.Status == "receiving" && item.Reasoning.Text != "" {
			live = true
			if !strings.Contains(item.Reasoning.Text, "[REDACTED]") {
				t.Fatal("live batch did not redact completed credentials")
			}
		}
	}
	if !live {
		t.Fatal("reasoning text was withheld until call completion")
	}
	if result.choice.ReasoningContent == nil || *result.choice.ReasoningContent != raw {
		t.Fatal("live display changed model continuation")
	}
}
