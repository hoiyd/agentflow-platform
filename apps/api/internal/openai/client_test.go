package openai

import (
	"strings"
	"testing"
)

func TestParseFallbackToolCall(t *testing.T) {
	call, ok := parseFallbackToolCall(`{"action":"tool_call","tool":"calculator","arguments":{"expression":"128 * 37"}}`)
	if !ok {
		t.Fatal("expected fallback tool call")
	}
	if call.Function.Name != "calculator" {
		t.Fatalf("expected calculator, got %s", call.Function.Name)
	}
	if call.Function.Arguments != `{"expression":"128 * 37"}` {
		t.Fatalf("unexpected arguments: %s", call.Function.Arguments)
	}
}

func TestParseFallbackToolCallFromFence(t *testing.T) {
	call, ok := parseFallbackToolCall("```json\n{\"action\":\"tool_call\",\"tool\":\"get_current_time\",\"arguments\":{\"timezone\":\"Asia/Shanghai\"}}\n```")
	if !ok {
		t.Fatal("expected fallback tool call")
	}
	if call.Function.Name != "get_current_time" {
		t.Fatalf("expected get_current_time, got %s", call.Function.Name)
	}
}

func TestNormalizeBaseURL(t *testing.T) {
	got := normalizeBaseURL("https://openrouter.ai/api/v1/")
	if got != "https://openrouter.ai/api/v1" {
		t.Fatalf("unexpected base url: %s", got)
	}
}

func TestBuildMessagesWithSystemPrompt(t *testing.T) {
	messages := buildMessagesWithSystemPrompt("You are a test agent.", nil, []string{"calculator"})
	if len(messages) != 1 {
		t.Fatalf("expected one system message, got %d", len(messages))
	}
	if messages[0].Role != "system" {
		t.Fatalf("expected system role, got %s", messages[0].Role)
	}
	if !strings.Contains(messages[0].Content, "You are a test agent.") {
		t.Fatalf("expected custom system prompt, got %q", messages[0].Content)
	}
}
