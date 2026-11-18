package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/config"
	"agentflow-platform/apps/api/internal/inference/routing"
)

func durableTestConfig(t *testing.T, root, database string, route routing.RouteConfig) config.Config {
	t.Helper()
	t.Setenv("BROWSER_FIXTURE_KEY", "fixture-only")
	t.Setenv("EMBEDDING_API_KEY", "fixture-only")
	route.Model, route.CredentialEnvironment = "fixture-model", "BROWSER_FIXTURE_KEY"
	route.ContextWindowTokens, route.MaxOutputTokens, route.Priority = 128000, 8192, 100
	route.Capabilities.ToolCalling, route.Capabilities.Streaming, route.Capabilities.StructuredOutput = true, true, true
	route.Pricing.Source = "fixture"
	data, err := json.Marshal(routing.RouteFile{Routes: []routing.RouteConfig{route}})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "routes.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Load()
	cfg.DatabaseURL = database
	cfg.ModelRouteConfigPath, cfg.ToolConfigPath = path, filepath.Join(root, "tools.json")
	cfg.AuthMode, cfg.TrustedSkillDirectories, cfg.SandboxEnabled = "local", "", false
	cfg.EmbeddingBaseURL, cfg.EmbeddingModel, cfg.EmbeddingDimensions = route.BaseURL, "fixture-embedding", 1536
	cfg.MemoryAdaptiveExtractionMode, cfg.ContextCompactionMode = "off", "off"
	cfg.RouterMode, cfg.AutonomousMaxIterations, cfg.ToolSchemaMode = "query_match", 1, "eager"
	cfg.ModelRetryMaxAttempts, cfg.ModelRequestsPerMinute, cfg.ModelTokensPerMinute = 1, 0, 0
	return cfg
}

func durableTestRequest(t *testing.T, server *httptest.Server, method, path string, input any, want int, out any) string {
	t.Helper()
	data, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(t.Context(), method, server.URL+path, bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 30 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != want {
		t.Fatalf("%s %s: %d %s", method, path, res.StatusCode, body)
	}
	if out != nil {
		if err := json.Unmarshal(body, out); err != nil {
			t.Fatalf("JSON: %v %s", err, body)
		}
	}
	return string(body)
}

func closeDurableTestApplication(application *Application) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return application.Close(ctx)
}
