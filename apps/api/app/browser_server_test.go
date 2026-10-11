package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/config"
	"agentflow-platform/apps/api/internal/identity"
	"agentflow-platform/apps/api/internal/testsupport/oidcfixture"
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
	capabilities := map[string]any{"tool_calling": true, "structured_output": true, "streaming": true}
	if os.Getenv("AGENTFLOW_REASONING_TEST") == "1" {
		capabilities["reasoning_display_format"] = "deepseek_reasoning_content"
	}
	pricing := map[string]any{"source": "fixture"}
	if os.Getenv("AGENTFLOW_USAGE_TEST") == "1" {
		pricing["input_per_million_tokens_micros"] = 2000000
		pricing["output_per_million_tokens_micros"] = 1000000
		pricing["cached_input_per_million_tokens_micros"] = 500000
	}
	routes, err := json.Marshal(map[string]any{"routes": []any{map[string]any{
		"id": "browser-fixture", "model": "fixture-model", "base_url": providerServer.URL,
		"credential_environment": "BROWSER_FIXTURE_KEY", "request_timeout_seconds": 45,
		"capabilities":          capabilities,
		"context_window_tokens": 128000, "max_output_tokens": 8192, "priority": 100,
		"pricing": pricing,
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(routePath, routes, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BROWSER_FIXTURE_KEY", "fixture-not-a-secret")
	t.Setenv("TAVILY_API_KEY", "")
	if os.Getenv("AGENTFLOW_PROMPT_SECURITY_TEST") == "1" {
		// Only makes the real Binding available. The gate must deny before HTTP;
		// its query is public and this fixture credential cannot authenticate.
		t.Setenv("TAVILY_API_KEY", "fixture-only")
	}
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
	// Discovery has its own opt-in gate; ordinary fixtures keep eager contracts.
	cfg.ToolSchemaMode = "eager"
	if os.Getenv("AGENTFLOW_TOOL_DISCOVERY_TEST") == "1" {
		cfg.ToolSchemaMode = "lazy"
	}
	if os.Getenv("AGENTFLOW_INBOX_EDGE_TEST") == "1" {
		cfg.ContextCompactionMode = "auto"
		cfg.ContextCompactionSoftThreshold, cfg.ContextCompactionHardThreshold = 0.001, 0.002
		cfg.ContextCompactionRecentTokens, cfg.ContextCompactionSummaryMaxTokens = 100, 200
		cfg.RunMaxModelCalls = 3
	}
	cfg.ModelRequestCaptureMode, cfg.ModelRetryMaxAttempts = "metadata_only", 1
	cfg.ModelRequestsPerMinute, cfg.ModelTokensPerMinute = 0, 0
	cfg.RouterMode, cfg.AutonomousMaxIterations = "query", 1
	cfg.AllowedOrigins = "http://127.0.0.1:13000"
	cfg.AuthMode = "local"
	cfg.SandboxEnabled = false
	// Always override operator reranker variables: only this opt-in gate calls
	// the controlled provider, and ordinary browser suites remain heuristic.
	cfg.RerankerMode = "heuristic"
	t.Setenv("RERANKER_API_KEY", "")
	if os.Getenv("AGENTFLOW_RERANKER_TEST") == "1" {
		cfg.RerankerMode, cfg.RerankerBaseURL = "tei", providerServer.URL
		cfg.RerankerModel, cfg.RerankerRevision = "fixture-cross-encoder", "fixture-revision"
		cfg.RerankerTimeout, cfg.RerankerMaxConcurrentRequests = time.Second, 2
	}
	var sandboxControls http.Handler
	if os.Getenv("AGENTFLOW_SANDBOX_BROWSER_TEST") == "1" {
		sandboxControls = browserSandboxConfig(t, &cfg, root)
	}
	if os.Getenv("AGENTFLOW_OWNER_ADMISSION_TEST") == "1" {
		cfg.MaxConcurrentModelRequests, cfg.MaxConcurrentOwnerModelRequests = 2, 1
		cfg.OwnerModelQueueSize, cfg.OwnerModelQueueWaitTimeout = 0, 3*time.Second
	}
	var oidcProvider *oidcfixture.Provider
	if os.Getenv("AGENTFLOW_IDENTITY_TEST") == "1" || os.Getenv("AGENTFLOW_EXECUTION_BOUNDARY_TEST") == "1" {
		t.Setenv("OIDC_CLIENT_SECRET", "fixture-only")
		oidcProvider = oidcfixture.New(t, "http://127.0.0.1:18080/api/auth/callback")
		oidcProvider.RequireSignIn()
		cfg.AuthMode, cfg.OIDCIssuer, cfg.OIDCClientID = "oidc", oidcProvider.Server.URL, "fixture-client"
		cfg.OIDCRedirectURL, cfg.AuthWebURL = "http://127.0.0.1:18080/api/auth/callback", "http://127.0.0.1:13000"
	}
	if os.Getenv("AGENTFLOW_KEYCLOAK_TEST") == "1" {
		// A separate disposable realm validates actual themed password forms, not
		// the signed fixture. Never connect to the operator's Keycloak realm.
		t.Setenv("OIDC_CLIENT_SECRET", "fixture-only")
		cfg.AuthMode, cfg.OIDCIssuer, cfg.OIDCClientID = "oidc", "http://127.0.0.1:19081/realms/agentflow-test", "fixture-client"
		cfg.OIDCRedirectURL, cfg.AuthWebURL = "http://127.0.0.1:18080/api/auth/callback", "http://127.0.0.1:13000"
	}
	cfg.VerificationAllowedCommands, cfg.VerificationAllowedHTTPHosts = "", ""
	cfg.VerificationWorkspaceRoot = root
	var boundary http.HandlerFunc
	if os.Getenv("AGENTFLOW_EXECUTION_BOUNDARY_TEST") == "1" {
		boundary = browserExecutionBoundary(t, &cfg, root)
	}
	application, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if oidcProvider != nil {
		pgfixture.GrantMemberships(t, cfg.DatabaseURL, cfg.OIDCIssuer, "fixture-operator", "Default workspace", "Workspace test")
		// An already-onboarded identity with revoked access must stay a nonmember.
		// A fresh identity would now receive a personal Workspace on first login.
		pgfixture.GrantMemberships(t, cfg.DatabaseURL, cfg.OIDCIssuer, "fixture-nonmember")
		id := identity.UserID(cfg.OIDCIssuer, "fixture-nonmember")
		db, err := sql.Open("pgx", cfg.DatabaseURL)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		if _, err := db.ExecContext(t.Context(), `INSERT INTO auth_personal_workspaces (user_id,workspace_id) VALUES ($1,NULL)`, id); err != nil {
			t.Fatal(err)
		}
	}
	production := application.server.Handler
	controls := http.NewServeMux()
	if os.Getenv("AGENTFLOW_INBOX_EDGE_TEST") == "1" {
		// Test-only history setup through the real Store, never an operator database.
		controls.HandleFunc("POST /__fixture/inbox/history/{id}", func(w http.ResponseWriter, r *http.Request) {
			for index := 0; index < 8; index++ {
				role := "user"
				if index%2 == 1 {
					role = "assistant"
				}
				if _, err := application.store.AddMessage(r.PathValue("id"), role, strings.Repeat("Historical fixture fact. ", 100)); err != nil {
					http.Error(w, err.Error(), 500)
					return
				}
			}
			w.WriteHeader(http.StatusNoContent)
		})
	}
	if sandboxControls != nil {
		controls.Handle("/__fixture/tool-progress/", sandboxControls)
	}
	if boundary != nil {
		controls.HandleFunc("GET /__fixture/execution-boundary", boundary)
	}
	if oidcProvider != nil {
		controls.HandleFunc("POST /__fixture/identity/subject", func(w http.ResponseWriter, r *http.Request) {
			var input struct {
				Subject string `json:"subject"`
			}
			if json.NewDecoder(r.Body).Decode(&input) != nil {
				w.WriteHeader(400)
				return
			}
			oidcProvider.SetSubject(input.Subject)
			w.WriteHeader(204)
		})
	}
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
