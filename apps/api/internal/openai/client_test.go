package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/tools"
)

func TestLocalClientPublicFallbacks(t *testing.T) {
	client := NewClientWithTimeoutAndEmbeddingModel("", "https://example.test/v1", "https://embed.example.test/v1", "gpt-test", "embed-test", 32, time.Second)
	if client.HasAPIKey() {
		t.Fatal("expected local client without API key")
	}
	identity := client.RuntimeIdentity()
	if identity.Provider != "openai_compatible" || identity.Model != "gpt-test" || identity.EmbeddingDimensions != 32 {
		t.Fatalf("unexpected runtime identity: %#v", identity)
	}
	localIdentity := identity
	localIdentity.BaseURL = "http://127.0.0.1:9999/v1"
	localIdentity.Model = "local-test"
	derived := client.WithRuntimeIdentity(localIdentity)
	if derived.RuntimeIdentity().Provider != "local" || derived.RuntimeIdentity().Model != "local-test" {
		t.Fatalf("unexpected derived runtime identity: %#v", derived.RuntimeIdentity())
	}

	catalog, err := tools.NewCatalog()
	if err != nil {
		t.Fatalf("new empty tool catalog: %v", err)
	}
	events, eventErrors := client.StreamAgentChatWithToolsTrace(
		context.Background(), "You are AgentFlow's assistant. Use tools when they help.", nil, "tool-free", catalog,
		nil, "", "", nil, nil,
	)
	var eventOutput strings.Builder
	for event := range events {
		if event.Type == "delta" {
			eventOutput.WriteString(event.Delta)
		}
	}
	if err := <-eventErrors; err != nil || !strings.Contains(eventOutput.String(), "You said: tool-free") {
		t.Fatalf("local event stream: output=%q err=%v", eventOutput.String(), err)
	}

	for _, testCase := range []struct {
		system string
		want   string
	}{
		{system: "You are the planner.", want: "Plan:"},
		{system: "You are the worker.", want: "Worker result:"},
		{system: "You are the reviewer.", want: "Review:"},
		{system: "You are the finalizer.", want: "Final answer:"},
		{system: "You are concise.", want: "Generated response:"},
	} {
		completion, err := client.CompleteText(context.Background(), testCase.system, "answer the task")
		if err != nil || !strings.Contains(completion, testCase.want) {
			t.Fatalf("complete with %q: output=%q err=%v", testCase.system, completion, err)
		}
	}

	embedding, err := client.EmbedText(context.Background(), "stable local embedding")
	if err != nil || len(embedding.Vector) != 32 || embedding.Provider != "local" || !embedding.Estimated {
		t.Fatalf("local embedding: embedding=%#v err=%v", embedding, err)
	}
	if _, err := client.EmbedText(context.Background(), "   "); err == nil {
		t.Fatal("expected blank embedding input rejection")
	}
}

func TestEmbeddingProtocols(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/v1/embeddings":
			if request.Header.Get("Authorization") != "Bearer test-key" {
				t.Errorf("missing authorization header: %q", request.Header.Get("Authorization"))
			}
			_, _ = response.Write([]byte(`{"model":"embed-server","data":[{"embedding":[0.25,0.75]}]}`))
		case "/api/embed":
			_, _ = response.Write([]byte(`{"model":"ollama-server","embeddings":[[0.4,0.6,0.8]]}`))
		default:
			http.NotFound(response, request)
		}
	}))
	t.Cleanup(server.Close)

	compatible := NewClientWithTimeoutAndEmbeddingModel("test-key", server.URL+"/v1", server.URL+"/v1", "chat-test", "embed-test", 2, time.Second)
	compatible.httpClient = server.Client()
	embedding, err := compatible.EmbedText(context.Background(), "semantic query")
	if err != nil || embedding.Model != "embed-server" || embedding.Provider != "openai_compatible" || len(embedding.Vector) != 2 {
		t.Fatalf("compatible embedding: embedding=%#v err=%v", embedding, err)
	}

	ollama := NewClientWithTimeoutAndEmbeddingModel("", server.URL+"/v1", server.URL+"/api/embed", "chat-test", "ollama-test", 3, time.Second)
	ollama.httpClient = server.Client()
	embedding, err = ollama.EmbedText(context.Background(), "semantic query")
	if err != nil || embedding.Model != "ollama-server" || embedding.Provider != "ollama" || len(embedding.Vector) != 3 {
		t.Fatalf("ollama embedding: embedding=%#v err=%v", embedding, err)
	}
}

func TestClientHelperContracts(t *testing.T) {
	history := []domain.Message{
		{ID: "user-1", Role: "user", Content: "first"},
		{ID: "ignored", Role: "system", Content: "ignore me"},
		{ID: "assistant-1", Role: "assistant", Content: "reply"},
		{ID: "user-2", Role: "user", Content: "latest"},
	}
	messages := buildMessages(history)
	if len(messages) != 4 || messages[3].Source != "current_input" || !strings.Contains(messages[0].Content, "calculator") {
		t.Fatalf("build default messages: %#v", messages)
	}
	withoutTools := buildMessagesWithToolNames(history, nil)
	if !strings.Contains(withoutTools[0].Content, "No tools") {
		t.Fatalf("build messages without tools: %#v", withoutTools[0])
	}
	if got := ensureCurrentInput(withoutTools, "latest"); len(got) != len(withoutTools) {
		t.Fatalf("matching current input should not append: %#v", got)
	}
	withReplacement := ensureCurrentInput(withoutTools, "replacement")
	if len(withReplacement) != len(withoutTools)+1 || withReplacement[len(withReplacement)-1].Content != "replacement" {
		t.Fatalf("replacement current input: %#v", withReplacement)
	}
	if got := ensureCurrentInput(withoutTools, "  "); len(got) != len(withoutTools) {
		t.Fatalf("blank current input should not append: %#v", got)
	}

	normalized := normalizeToolCalls([]ToolCall{{Function: FunctionCall{Name: "calculator"}}})
	if normalized[0].ID != "call_1" || normalized[0].Type != "function" || normalized[0].Function.Arguments != "{}" {
		t.Fatalf("normalize tool calls: %#v", normalized)
	}
	if summarizeToolResults(nil) != "Tool execution completed." || summarizeToolResults([]tools.ExecutionResult{{Tool: "calculator"}}) != "Tool execution completed." {
		t.Fatal("unexpected tool result summary")
	}

	events := make(chan StreamEvent, 8)
	if err := emitText(context.Background(), "one two", events); err != nil {
		t.Fatalf("emit text: %v", err)
	}
	close(events)
	var emitted strings.Builder
	for event := range events {
		emitted.WriteString(event.Delta)
	}
	if emitted.String() != "one two" {
		t.Fatalf("emitted text = %q", emitted.String())
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := emitText(canceled, "cancel me", make(chan StreamEvent)); err != context.Canceled {
		t.Fatalf("expected canceled emit, got %v", err)
	}

	payload := retrievedMemoryPayload([]domain.RetrievedMemory{{
		Memory:     domain.Memory{ID: "memory-1", Kind: "fact", Content: strings.Repeat("x", 1300), Metadata: map[string]any{"topic": "coverage"}, ConversationID: "conversation-1", RunID: "run-1"},
		Similarity: 0.8, RecencyBoost: 0.1, Score: 0.9,
	}})
	if len(payload) != 1 || payload[0]["id"] != "memory-1" || len(payload[0]["content"].(string)) != 1203 {
		t.Fatalf("retrieved memory payload: %#v", payload)
	}
	usage := Usage{PromptTokens: 2, CompletionTokens: 3, TotalTokens: 5, Estimated: true}
	if !usage.Valid() || (Usage{}).Valid() {
		t.Fatal("unexpected usage validity")
	}
	tokens := tokenPayload(map[string]any{"model": "test"}, usage)
	if tokens["total_tokens"] != 5 || tokens["token_usage_estimated"] != true {
		t.Fatalf("token payload: %#v", tokens)
	}
}

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
