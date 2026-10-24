package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/config"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/identity"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/testsupport/pgfixture"
	"agentflow-platform/apps/api/internal/tool"
	"agentflow-platform/apps/api/internal/tool/policy"
)

// Failure inventory: new defaults must not contaminate a fixed old configuration;
// startup/readiness, actual grants and receipts survive restart without rewriting
// that file; revocation prevents execution; invalid rules reject startup and
// release the sandbox lock so a corrected configuration can start normally.
func TestToolPolicyConfigUpgradeStartsApplication(t *testing.T) {
	database := pgfixture.DatabaseURL(t)
	fixturePath := filepath.Join("testdata", "tools.pre-web-fetch.json")
	original, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	var expected tool.Config
	if err := json.Unmarshal(original, &expected); err != nil {
		t.Fatal(err)
	}
	expected.SecurityPolicy = policy.NormalizePolicy(expected.SecurityPolicy)
	revision, err := exec.Command("git", "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	dirty, err := exec.Command("git", "status", "--porcelain").Output()
	if err != nil {
		t.Fatal(err)
	}
	evidenceDir := os.Getenv("EVALUATION_REPORT_DIR")
	if evidenceDir != "" {
		evidenceDir, err = filepath.Abs(evidenceDir)
		if err != nil {
			t.Fatal(err)
		}
	}
	root := t.TempDir()
	t.Chdir(root)
	provider := newBrowserProvider()
	model := httptest.NewServer(http.HandlerFunc(provider.respond))
	t.Cleanup(model.Close)
	t.Setenv("POLICY_FIXTURE_KEY", "fixture-only")
	t.Setenv("EMBEDDING_API_KEY", "fixture-only")
	t.Setenv("TAVILY_API_KEY", "")
	t.Setenv("MCP_CREDENTIAL_ENCRYPTION_KEY", "")
	cfg := config.Load()
	cfg.DatabaseURL, cfg.AuthMode = database, "local"
	cfg.ModelRouteConfigPath, cfg.ToolConfigPath = filepath.Join(root, "routes.json"), filepath.Join(root, "tools.json")
	cfg.TrustedSkillDirectories, cfg.MCPConfigPath, cfg.MCPWorkspaceLocalOrigins, cfg.FilesystemRootsPath, cfg.ProjectGuidancePath = "", "", "", "", ""
	cfg.OTelTracesExporter, cfg.ContextCompactionMode, cfg.MemoryAdaptiveExtractionMode = "none", "off", "off"
	cfg.RerankerMode, cfg.ToolSchemaMode, cfg.ModelRetryMaxAttempts = "heuristic", "eager", 1
	cfg.ModelRequestsPerMinute, cfg.ModelTokensPerMinute = 0, 0
	cfg.EmbeddingBaseURL, cfg.EmbeddingModel, cfg.EmbeddingDimensions = model.URL, "fixture-embedding", 1536
	cfg.SandboxEnabled, cfg.SandboxAllowedCommands = true, "/usr/bin/python3"
	cfg.SandboxExecutable, cfg.SandboxStateDirectory = filepath.Join(root, "sbx-fixture"), filepath.Join(root, "sandboxes")
	invocations := filepath.Join(root, "invocations")
	script := fmt.Sprintf(`#!/bin/sh
case "$1" in
  create)
    if [ "$2" = shell ]; then printf '%%s' '--skills --deny-network --cpus --memory --template'; exit 0; fi
    case " $* " in *" --deny-network ** "*) ;; *) exit 91;; esac
    printf 'create\n' >> %q;;
  exec) printf 'exec\n' >> %q; printf 'sandbox browser receipt';;
  rm) printf 'cleanup\n' >> %q;;
  ls) exit 0;;
  *) exit 92;;
esac
`, invocations, invocations, invocations)
	if err := os.WriteFile(cfg.SandboxExecutable, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	writeHeadlessJSON(t, cfg.ModelRouteConfigPath, map[string]any{"routes": []any{map[string]any{
		"id": "policy-fixture", "base_url": model.URL, "model": "fixture-model", "credential_environment": "POLICY_FIXTURE_KEY",
		"request_timeout_seconds": 10, "context_window_tokens": 128000, "max_output_tokens": 8192, "priority": 100,
		"capabilities": map[string]bool{"tool_calling": true, "streaming": true}, "pricing": map[string]string{"source": "fixture"},
	}}})
	if err := os.WriteFile(cfg.ToolConfigPath, original, 0600); err != nil {
		t.Fatal(err)
	}
	application, err := New(cfg)
	if err != nil {
		t.Fatal("fixed old configuration cannot start the production application", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := application.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	storage, err := store.NewPostgresStore(database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { storage.Close() })
	workspace, err := storage.DefaultWorkspace(t.Context(), identity.SuperUserID)
	if err != nil {
		t.Fatal(err)
	}
	request := func(method, path string, body any, status int) []byte {
		t.Helper()
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest(method, path, bytes.NewReader(encoded))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Workspace-ID", workspace)
		response := httptest.NewRecorder()
		application.ServeHTTP(response, r)
		if response.Code != status || status == 200 && strings.Contains(response.Body.String(), "event: error") {
			t.Fatalf("%s %s: %d %s", method, path, response.Code, response.Body.String())
		}
		return response.Body.Bytes()
	}
	checkTools := func(allowed bool) {
		t.Helper()
		request(http.MethodGet, "/health", nil, 200)
		var items []tool.ToolInfo
		if err := json.Unmarshal(request(http.MethodGet, "/api/tools", nil, 200), &items); err != nil {
			t.Fatal(err)
		}
		found := false
		for _, item := range items {
			if item.Name == "sandbox_command" {
				found = true
				if item.Enabled != allowed || !item.ServiceEnabled || !allowed && item.ExcludedReason != "policy_denied" {
					t.Fatalf("operator sandbox grant changed: %+v", item)
				}
			}
			if item.Name == "web_fetch" && item.ServiceEnabled {
				t.Fatal("old explicit configuration acquired the new default Tool")
			}
		}
		if !found {
			t.Fatal("sandbox Binding missing from production catalog")
		}
	}
	checkTools(true)
	loaded, err := tool.LoadConfig(cfg.ToolConfigPath)
	if err != nil || !reflect.DeepEqual(loaded, expected) {
		t.Fatalf("old configuration inherited current defaults: %+v %v", loaded, err)
	}
	var owned domain.Agent
	agentInput := map[string]any{"name": "Policy upgrade fixture", "tools": []string{"sandbox_command"}, "memory_enabled": false, "retrieval_enabled": false}
	if err := json.Unmarshal(request(http.MethodPost, "/api/agents", agentInput, 201), &owned); err != nil {
		t.Fatal(err)
	}
	conversation, err := storage.CreateConversationInWorkspace(workspace, "Configuration upgrade fixture")
	if err != nil {
		t.Fatal(err)
	}
	request(http.MethodPost, "/api/chat", domain.ChatRequest{ConversationID: conversation.ID, AgentID: owned.ID, Mode: "single", Message: "sandbox-gate: validate the existing operator grant"}, 200)
	runs, err := storage.ListRuns()
	if err != nil || len(runs) != 1 || runs[0].Status != domain.RunCompleted {
		t.Fatalf("real sandbox Run did not complete: %+v %v", runs, err)
	}
	run := runs[0]
	effects, err := storage.ListToolEffects(run.ID)
	if err != nil || len(effects) != 1 || effects[0].Status != domain.ToolEffectCommitted || effects[0].TurnID == "" || effects[0].StageID != "" {
		t.Fatalf("Single sandbox receipt lost actual Turn identity: %+v %v", effects, err)
	}
	if run.RuntimeSnapshot == nil {
		t.Fatal("completed Run has no frozen policy")
	}
	foundSandbox := false
	for _, rule := range run.RuntimeSnapshot.ToolSecurityPolicy.Rules {
		if rule.Tool == "sandbox_command" {
			foundSandbox = true
			if !reflect.DeepEqual(policy.NormalizeCapability(rule.Capability), expected.SecurityPolicy.Rules[2].Capability) {
				t.Fatalf("frozen sandbox authority differs from operator JSON: %+v", rule)
			}
		}
	}
	if !foundSandbox {
		t.Fatal("sandbox grant missing from frozen policy")
	}
	restart := func() {
		t.Helper()
		if err := application.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
		application, err = New(cfg)
		if err != nil {
			t.Fatal("restart from existing configuration", err)
		}
	}
	restart()
	checkTools(true)
	replay := request(http.MethodGet, "/api/runs/"+run.ID+"/replay", nil, 200)
	if !bytes.Contains(replay, []byte("sandbox browser receipt")) || !bytes.Contains(replay, []byte(`"cleanup_confirmed":true`)) {
		t.Fatal("sandbox result or cleanup receipt lost after reopening Postgres")
	}
	if data, err := os.ReadFile(cfg.ToolConfigPath); err != nil || !bytes.Equal(data, original) {
		t.Fatal("startup rewrote the operator configuration", err)
	}
	expected.SecurityPolicy.Rules[2].Action = policy.ActionDeny
	writeHeadlessJSON(t, cfg.ToolConfigPath, expected)
	restart()
	checkTools(false)
	request(http.MethodPost, "/api/agents", agentInput, 400)
	if calls, err := os.ReadFile(invocations); err != nil || string(calls) != "create\nexec\ncleanup\n" {
		t.Fatalf("unexpected sandbox invocations: %s %v", calls, err)
	}
	if err := application.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{
		`{"security_policy":{"rules":[null]}}`,
		`{"security_policy":{"rules":[{"id":"bad","tool":"sandbox_command","action":"allow_and_log","capability":{"scope":{"network":{"mode":"none","targets":["example.test"]}}}}]}}`,
	} {
		if err := os.WriteFile(cfg.ToolConfigPath, []byte(invalid), 0600); err != nil {
			t.Fatal(err)
		}
		if unexpected, err := New(cfg); err == nil {
			_ = unexpected.Close(t.Context())
			t.Fatal("invalid security configuration started successfully")
		} else if !strings.Contains(err.Error(), "validate Tool security policy") {
			t.Fatal("invalid configuration failed at the wrong boundary", err)
		}
	}
	if err := os.WriteFile(cfg.ToolConfigPath, original, 0600); err != nil {
		t.Fatal(err)
	}
	application, err = New(cfg)
	if err != nil {
		t.Fatal("rejected startup leaked the sandbox ownership lock", err)
	}
	checkTools(true)
	provider.mu.Lock()
	failures := append([]string(nil), provider.failures...)
	provider.mu.Unlock()
	if len(failures) != 0 {
		t.Fatalf("provider wire contract failed: %v", failures)
	}
	if evidenceDir != "" {
		if err := os.MkdirAll(evidenceDir, 0700); err != nil {
			t.Fatal(err)
		}
		writeHeadlessJSON(t, filepath.Join(evidenceDir, "tool-policy-config-upgrade.json"), map[string]any{
			"schema_version": "tool-policy-config-upgrade-evidence-v1", "git_revision": strings.TrimSpace(string(revision)), "working_tree_dirty": len(dirty) != 0,
			"fixture": "app/" + filepath.ToSlash(fixturePath), "fixture_sha256": fmt.Sprintf("%x", sha256.Sum256(original)),
			"test": t.Name(), "workspace_id": workspace, "agent_id": owned.ID, "run_id": run.ID, "run_status": run.Status, "receipt_status": effects[0].Status,
			"checks":      []string{"unchanged old policy and file", "sandbox network remains denied", "new default Tool remains disabled", "real Binding receipt survives restart", "operator revocation rejects Agent binding", "invalid startup rejects and releases lock"},
			"limitations": []string{"synthetic configuration and model", "controlled sbx CLI process, not live VM isolation", "Single mode; parser/order regressions run separately", "uncommitted evidence is not an immutable revision"},
		})
	}
}
