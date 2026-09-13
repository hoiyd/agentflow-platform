package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/config"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/modelrouting"
	"agentflow-platform/apps/api/internal/openai"
	"agentflow-platform/apps/api/internal/testsupport/pgfixture"
)

type answerRelevanceEmbeddingClientStub struct {
	embedding openai.Embedding
	err       error
	input     string
}

func (s *answerRelevanceEmbeddingClientStub) EmbedText(_ context.Context, input string) (openai.Embedding, error) {
	s.input = input
	return s.embedding, s.err
}

func TestSplitOrigins(t *testing.T) {
	got := splitOrigins(" http://localhost:3000,https://agentflow.example.com, ,")
	want := []string{"http://localhost:3000", "https://agentflow.example.com"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("split origins: got %#v want %#v", got, want)
	}
}

func TestNewStoreRequiresPostgresWithoutFallback(t *testing.T) {
	t.Setenv("STORE_DRIVER", "file")
	t.Setenv("DATA_PATH", t.TempDir()+"/state.json")
	for _, databaseURL := range []string{"", "://invalid"} {
		backend, err := newStore(config.Config{DatabaseURL: databaseURL})
		if err == nil {
			t.Fatalf("invalid PostgreSQL configuration must fail, backend=%T err=%v", backend, err)
		}
		if databaseURL == "" && !strings.Contains(err.Error(), "DATABASE_URL is required") {
			t.Fatalf("missing configuration is not actionable: %v", err)
		}
	}
}

func TestContextAssemblyConfigMapsAllSettings(t *testing.T) {
	cfg := config.Config{
		ModelContextWindowTokens:          101,
		ModelOutputReserveTokens:          102,
		ContextSafetyMarginTokens:         103,
		ContextHistoryMaxTokens:           104,
		ContextMemoryMaxTokens:            105,
		ContextKnowledgeMaxTokens:         106,
		ContextToolResultMaxTokens:        107,
		ContextHistoryRetrievalMaxResults: 8,
		ContextHistoryRetrievalMaxChars:   1200,
		ContextHistoryRetrievalMaxTokens:  300,
		ContextHistoryRetrievalWindow:     2,
		ContextCompactionMode:             "off",
		ContextCompactionSoftThreshold:    0.61,
		ContextCompactionHardThreshold:    0.82,
		ContextCompactionRecentTokens:     108,
		ContextCompactionSummaryMaxTokens: 109,
		ContextCompactionTimeout:          110 * time.Millisecond,
	}
	want := domain.ContextAssemblyConfig{
		ContextWindowTokens:        101,
		OutputReserveTokens:        102,
		SafetyMarginTokens:         103,
		HistoryMaxTokens:           104,
		MemoryMaxTokens:            105,
		KnowledgeMaxTokens:         106,
		ToolResultMaxTokens:        107,
		HistoryRetrievalEnabled:    true,
		HistoryRetrievalMaxResults: 8,
		HistoryRetrievalMaxChars:   1200,
		HistoryRetrievalMaxTokens:  300,
		HistoryRetrievalWindow:     2,
		CompactionMode:             "off",
		CompactionSoftThreshold:    0.61,
		CompactionHardThreshold:    0.82,
		CompactionRecentTokens:     108,
		CompactionSummaryMaxTokens: 109,
		CompactionTimeoutMS:        110,
	}

	if got := contextAssemblyConfig(cfg); !reflect.DeepEqual(got, want) {
		t.Fatalf("context assembly config: got %#v want %#v", got, want)
	}
}

func TestModelRouteCatalogTreatsConfiguredModelsAsPeers(t *testing.T) {
	generalClient := openai.NewClient("", "https://general.example/v1", "general-chat")
	priorityClient := openai.NewClient("", "https://priority.example/v1", "priority-chat")
	general := modelrouting.RouteConfig{
		ID: "general", BaseURL: "https://general.example/v1", Model: "general-chat",
		CredentialEnvironment: "GENERAL_MODEL_API_KEY", RequestTimeoutSeconds: 300,
		Capabilities:        modelrouting.Capabilities{ToolCalling: true, StructuredOutput: true, Streaming: true},
		ContextWindowTokens: 128000, MaxOutputTokens: 4096, Priority: 100,
		Pricing: modelrouting.Pricing{Source: "test_fixture"},
	}
	route := modelrouting.RouteConfig{
		ID: "priority", BaseURL: "https://priority.example/v1", Model: "priority-chat",
		CredentialEnvironment: "PRIORITY_MODEL_API_KEY", RequestTimeoutSeconds: 300,
		Capabilities:        modelrouting.Capabilities{StructuredOutput: true, Streaming: true},
		ContextWindowTokens: 64000, MaxOutputTokens: 4096, Priority: 120,
		Pricing: modelrouting.Pricing{Source: "test_fixture"},
	}
	catalog, err := modelrouting.NewCatalog(modelrouting.Binding{
		Descriptor: general.Descriptor(generalClient.RuntimeIdentity().Provider), Client: generalClient,
	}, modelrouting.Binding{
		Descriptor: route.Descriptor(priorityClient.RuntimeIdentity().Provider), Client: priorityClient,
	})
	if err != nil {
		t.Fatal(err)
	}
	descriptors := catalog.Descriptors()
	if len(descriptors) != 2 || descriptors[1].ID != "priority" || descriptors[1].Model != "priority-chat" ||
		descriptors[1].Priority != 120 || descriptors[1].Capabilities.ToolCalling ||
		!descriptors[1].Capabilities.StructuredOutput || descriptors[1].CredentialEnvironment != "PRIORITY_MODEL_API_KEY" {
		t.Fatalf("unexpected model route catalog: %#v", descriptors)
	}
	decision, err := catalog.Select(domain.ModelRouteRequirements{Purpose: "primary", Streaming: true, MaxOutputTokens: 4096})
	if err != nil || decision.Route.ID != "priority" {
		t.Fatalf("configured priority did not select priority route: route=%q err=%v", decision.Route.ID, err)
	}
	decision, err = catalog.Select(domain.ModelRouteRequirements{
		Purpose: "primary", Streaming: true, EstimatedInputTokens: 70000, MaxOutputTokens: 4096,
	})
	if err != nil || decision.Route.ID != "general" {
		t.Fatalf("capacity filter did not retain the compatible general route: route=%q err=%v", decision.Route.ID, err)
	}
}

func TestAnswerRelevanceEmbedderMapsEmbeddingAndError(t *testing.T) {
	client := &answerRelevanceEmbeddingClientStub{embedding: openai.Embedding{
		Vector: []float64{0.1, 0.2}, Model: "embedding-model", Provider: "provider",
		Estimated: true, Dimensions: 2,
	}}
	embedding, err := newAnswerRelevanceEmbedder(client)(context.Background(), "question")
	if err != nil {
		t.Fatalf("embed answer relevance input: %v", err)
	}
	if client.input != "question" || !reflect.DeepEqual(embedding.Vector, client.embedding.Vector) ||
		embedding.Model != "embedding-model" || embedding.Provider != "provider" ||
		!embedding.Estimated || embedding.Dimensions != 2 {
		t.Fatalf("embedding adapter lost metadata: %#v", embedding)
	}

	want := errors.New("embedding unavailable")
	client.err = want
	if _, err := newAnswerRelevanceEmbedder(client)(context.Background(), "question"); !errors.Is(err, want) {
		t.Fatalf("embedding adapter did not preserve error: %v", err)
	}
}

func TestNewApplicationWiresHealthRoute(t *testing.T) {
	routePath := filepath.Join(t.TempDir(), "model-routes.json")
	if err := os.WriteFile(routePath, []byte(`{"routes":[{"id":"test","base_url":"https://api.openai.com/v1","model":"test-model","credential_environment":"TEST_MODEL_API_KEY","request_timeout_seconds":1,"capabilities":{"tool_calling":true,"structured_output":true,"streaming":true},"context_window_tokens":128000,"max_output_tokens":8192,"priority":100,"pricing":{"source":"test"}}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TEST_MODEL_API_KEY", "fixture-key")
	cfg := config.Config{
		Port:                       "0",
		ModelRouteConfigPath:       routePath,
		EmbeddingBaseURL:           "http://localhost:11434/api/embed",
		EmbeddingModel:             "test-embedding",
		EmbeddingDimensions:        1536,
		EmbeddingRequestTimeout:    time.Second,
		MaxConcurrentRuns:          1,
		RunQueueSize:               1,
		RunQueueWaitTimeout:        time.Second,
		MaxConcurrentModelRequests: 1,
		ModelRetryMaxAttempts:      1,
		ModelRetryBaseDelay:        time.Millisecond,
		ModelRetryMaxDelay:         time.Millisecond,
		DatabaseURL:                pgfixture.DatabaseURL(t),
		AllowedOrigins:             "http://localhost:3000",
	}

	application, err := New(cfg)
	if err != nil {
		t.Fatalf("new application: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := application.Close(ctx); err != nil {
			t.Errorf("close application: %v", err)
		}
	})

	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	response := httptest.NewRecorder()
	application.server.Handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("health status: got %d body=%s", response.Code, response.Body.String())
	}
	if got := response.Body.String(); got != "{\"status\":\"ok\"}\n" {
		t.Fatalf("health body: got %q", got)
	}
}
