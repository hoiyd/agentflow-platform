package openai

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
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

func TestEmbeddingRequestPayloadSendsConfiguredDimensions(t *testing.T) {
	var requestBody map[string]any
	client := NewClientWithTimeoutAndEmbeddingModel("test-key", "https://example.test/v1", "gpt-test", "text-embedding-3-large", 1536, time.Second)
	payload, err := client.embeddingRequestPayload("semantic search")
	if err != nil {
		t.Fatalf("build embedding request: %v", err)
	}
	if err := json.Unmarshal(payload, &requestBody); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	if requestBody["model"] != "text-embedding-3-large" {
		t.Fatalf("expected embedding model in request, got %#v", requestBody)
	}
	if requestBody["dimensions"] != float64(1536) {
		t.Fatalf("expected dimensions=1536 in request, got %#v", requestBody)
	}
}

func TestBuildMessagesWithSystemPrompt(t *testing.T) {
	messages := buildMessagesWithSystemPrompt("You are a test agent.", nil, []string{"calculator"}, nil, nil)
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

func TestBuildMessagesWithRetrievedMemories(t *testing.T) {
	messages := buildMessagesWithSystemPrompt("You are a test agent.", nil, nil, []domain.RetrievedMemory{{
		Memory: domain.Memory{
			ID:      "mem_1",
			Kind:    "note",
			Content: "User prefers pgvector-backed memory search.",
		},
		Similarity: 0.9,
		Score:      0.95,
	}}, nil)
	if len(messages) != 1 {
		t.Fatalf("expected one system message, got %d", len(messages))
	}
	if !strings.Contains(messages[0].Content, "Retrieved memories") {
		t.Fatalf("expected retrieved memory section, got %q", messages[0].Content)
	}
	if !strings.Contains(messages[0].Content, "User prefers pgvector-backed memory search.") {
		t.Fatalf("expected memory content, got %q", messages[0].Content)
	}
}

func TestBuildMessagesWithRetrievedChunks(t *testing.T) {
	messages := buildMessagesWithSystemPrompt("You are a test agent.", nil, nil, nil, []domain.RetrievedDocumentChunk{{
		Document: domain.Document{ID: "doc_1", Title: "Deploy Notes"},
		Chunk: domain.DocumentChunk{
			ID:      "chunk_1",
			Content: "The deployment password is amber-9137.",
		},
		Similarity: 0.91,
		Score:      0.94,
	}})
	if len(messages) != 1 {
		t.Fatalf("expected one system message, got %d", len(messages))
	}
	if !strings.Contains(messages[0].Content, "Retrieved document chunks") {
		t.Fatalf("expected retrieved chunk section, got %q", messages[0].Content)
	}
	if !strings.Contains(messages[0].Content, "amber-9137") {
		t.Fatalf("expected chunk content, got %q", messages[0].Content)
	}
}
