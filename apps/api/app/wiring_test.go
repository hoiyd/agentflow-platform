package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/config"
	"agentflow-platform/apps/api/internal/domain"
)

func TestSplitOrigins(t *testing.T) {
	got := splitOrigins(" http://localhost:3000,https://agentflow.example.com, ,")
	want := []string{"http://localhost:3000", "https://agentflow.example.com"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("split origins: got %#v want %#v", got, want)
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

func TestNewApplicationWiresHealthRoute(t *testing.T) {
	cfg := config.Config{
		Port:                       "0",
		OpenAIBaseURL:              "https://api.openai.com/v1",
		OpenAIModel:                "test-model",
		EmbeddingBaseURL:           "http://localhost:11434/api/embed",
		EmbeddingModel:             "test-embedding",
		EmbeddingDimensions:        1536,
		OpenAITimeout:              time.Second,
		MaxConcurrentRuns:          1,
		RunQueueSize:               1,
		RunQueueWaitTimeout:        time.Second,
		MaxConcurrentModelRequests: 1,
		ModelRetryMaxAttempts:      1,
		ModelRetryBaseDelay:        time.Millisecond,
		ModelRetryMaxDelay:         time.Millisecond,
		StoreDriver:                "file",
		DataPath:                   filepath.Join(t.TempDir(), "agentflow.json"),
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
