package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/config"
	"agentflow-platform/apps/api/internal/testsupport/pgfixture"
)

// TestBrowserServer is an opt-in host for real browser-to-Postgres tests. Keeping
// it in a _test.go file prevents fixture routes/dependencies entering the server.
func TestBrowserServer(t *testing.T) {
	if os.Getenv("AGENTFLOW_BROWSER_TEST") != "1" {
		t.Skip("started only by the browser functional suite")
	}
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Fatal("browser tests require TEST_DATABASE_URL; never use the application database")
	}
	fixture := newBrowserProvider()
	providerServer := httptest.NewServer(http.HandlerFunc(fixture.respond))
	t.Cleanup(providerServer.Close)
	root := t.TempDir()
	t.Chdir(root)
	skillDir := filepath.Join(root, "skills", "fixture-method")
	if err := os.MkdirAll(skillDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: fixture-method\ndescription: Maintain structured evidence\n---\nFIXTURE_SKILL_BODY: update_task_state uses nested task.details and expected_version."), 0600); err != nil {
		t.Fatal(err)
	}
	routePath := filepath.Join(root, "routes.json")
	routes, err := json.Marshal(map[string]any{"routes": []any{map[string]any{
		"id": "browser-fixture", "model": "fixture-model", "base_url": providerServer.URL,
		"credential_environment": "BROWSER_FIXTURE_KEY", "request_timeout_seconds": 45,
		"capabilities":          map[string]bool{"tool_calling": true, "structured_output": true, "streaming": true},
		"context_window_tokens": 128000, "max_output_tokens": 8192, "priority": 100,
		"pricing": map[string]string{"source": "fixture"},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(routePath, routes, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BROWSER_FIXTURE_KEY", "fixture-not-a-secret")
	t.Setenv("TAVILY_API_KEY", "")
	t.Setenv("EMBEDDING_API_KEY", "fixture-not-a-secret")
	// Load defaults from the empty temporary directory (never an operator .env), then
	// explicitly bind every external resource to fixtures, never operator data.
	cfg := config.Load()
	cfg.BindAddress, cfg.Port = "127.0.0.1", "18080"
	cfg.DatabaseURL = pgfixture.DatabaseURL(t)
	cfg.ModelRouteConfigPath, cfg.ToolConfigPath = routePath, filepath.Join(root, "tools.json")
	cfg.TrustedSkillDirectories = filepath.Dir(skillDir)
	cfg.EmbeddingBaseURL, cfg.EmbeddingModel, cfg.EmbeddingDimensions = providerServer.URL, "fixture-embedding", 1536
	cfg.MemoryAdaptiveExtractionMode, cfg.ContextCompactionMode = "off", "off"
	cfg.ModelRequestCaptureMode, cfg.ModelRetryMaxAttempts = "metadata_only", 1
	cfg.ModelRequestsPerMinute, cfg.ModelTokensPerMinute = 0, 0
	cfg.RouterMode, cfg.AutonomousMaxIterations = "query", 1
	cfg.AllowedOrigins = "http://127.0.0.1:13000"
	cfg.VerificationAllowedCommands, cfg.VerificationAllowedHTTPHosts = "", ""
	cfg.VerificationWorkspaceRoot = root
	application, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	production := application.server.Handler
	controls := http.NewServeMux()
	controls.HandleFunc("GET /__fixture/contracts", fixture.contracts)
	controls.HandleFunc("POST /__fixture/release", fixture.release)
	controls.Handle("/", production)
	application.server.Handler = controls
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := application.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := application.Run(ctx); err != nil {
		t.Fatal(err)
	}
}
