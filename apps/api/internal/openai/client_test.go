package openai

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
)

func TestRetrievedChunkPayloadIncludesFusionRanksAndSourceDetails(t *testing.T) {
	items := retrievedChunkPayload([]domain.RetrievedDocumentChunk{{
		Chunk: domain.DocumentChunk{ID: "chunk-1", ChunkSource: domain.ChunkSource{
			ParentID: "parent-1", SectionPath: []string{"Guide", "Deploy"},
			StartOffset: 12, EndOffset: 48, DocumentVersion: "v1", ContentHash: "hash-1"}},
		VectorRank:  2,
		LexicalRank: 1,
		RRFScore:    0.0325,
		FusionRank:  1,
		RerankRank:  2,
	}})
	if len(items) != 1 {
		t.Fatalf("expected one retrieved chunk payload, got %#v", items)
	}
	if items[0]["vector_rank"] != 2 || items[0]["lexical_rank"] != 1 || items[0]["rrf_score"] != 0.0325 || items[0]["fusion_rank"] != 1 || items[0]["rerank_rank"] != 2 {
		t.Fatalf("expected recall, fusion, and rerank fields, got %#v", items[0])
	}
	if items[0]["parent_id"] != "parent-1" || items[0]["document_version"] != "v1" || items[0]["content_hash"] != "hash-1" || items[0]["start_offset"] != 12 || items[0]["end_offset"] != 48 {
		t.Fatalf("expected source details, got %#v", items[0])
	}
}

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

func TestSafeRuntimeURLPreservesRequiredQueryParameters(t *testing.T) {
	got := safeRuntimeURL("https://example.test/openai/deployments/test?api-version=2025-04-01-preview&token=private#fragment")
	if got != "https://example.test/openai/deployments/test?api-version=2025-04-01-preview" {
		t.Fatalf("unexpected safe runtime url: %s", got)
	}
}

func TestSafeRuntimeURLRemovesCredentials(t *testing.T) {
	got := safeRuntimeURL("https://user:password@example.test/v1?api_key=private&client-secret=secret&auth_token=private&sig=secret")
	if got != "https://example.test/v1" {
		t.Fatalf("unexpected safe runtime url: %s", got)
	}
}

func TestEmbeddingRequestPayloadSendsConfiguredDimensions(t *testing.T) {
	var requestBody map[string]any
	client := NewClientWithTimeoutAndEmbeddingModel("test-key", "https://example.test/v1", "https://embed.example.test/v1", "gpt-test", "text-embedding-3-large", 1536, time.Second)
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

func TestEmbeddingBaseURLDefaultsToOllama(t *testing.T) {
	client := NewClientWithTimeoutAndEmbeddingModel("test-key", "https://example.test/v1", "", "gpt-test", "text-embedding-3-large", 1536, time.Second)
	if client.embeddingBaseURL != defaultEmbeddingBaseURL {
		t.Fatalf("expected embedding base url to default to ollama, got %q", client.embeddingBaseURL)
	}
}

func TestOllamaEmbeddingRequestPayload(t *testing.T) {
	var requestBody map[string]any
	client := NewClientWithTimeoutAndEmbeddingModel("test-key", "https://example.test/v1", "http://localhost:11434/api/embed", "gpt-test", "embeddinggemma", 1536, time.Second)
	payload, err := client.ollamaEmbeddingRequestPayload("semantic search")
	if err != nil {
		t.Fatalf("build ollama embedding request: %v", err)
	}
	if err := json.Unmarshal(payload, &requestBody); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	if requestBody["model"] != "embeddinggemma" {
		t.Fatalf("expected ollama embedding model in request, got %#v", requestBody)
	}
	if requestBody["input"] != "semantic search" {
		t.Fatalf("expected ollama input in request, got %#v", requestBody)
	}
	if requestBody["dimensions"] != float64(1536) {
		t.Fatalf("expected ollama dimensions=1536 in request, got %#v", requestBody)
	}
	if !client.usesOllamaEmbedEndpoint() {
		t.Fatal("expected /api/embed endpoint to use ollama embedding mode")
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
