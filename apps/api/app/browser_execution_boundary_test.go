package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"agentflow-platform/apps/api/internal/config"
)

// Only an opt-in test composition exposes these destinations. Requests still
// traverse the production verifier and governed network transport.
func browserExecutionBoundary(t *testing.T, cfg *config.Config, root string) http.HandlerFunc {
	var allowedHits, forbiddenHits atomic.Int32
	forbidden := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		forbiddenHits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(forbidden.Close)
	allowed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		allowedHits.Add(1)
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, forbidden.URL, 302)
			return
		}
		if r.URL.Path == "/oversized" {
			_, _ = w.Write(make([]byte, (1<<20)+1))
			return
		}
		_, _ = w.Write([]byte("boundary verified"))
	}))
	t.Cleanup(allowed.Close)
	cfg.VerificationAllowedHTTPHosts = allowed.URL
	// An actual executable is allowlisted to prove OIDC gating, not just an empty
	// allowlist. The request cannot turn this host command into a remote capability.
	cfg.VerificationAllowedCommands = "/usr/bin/touch"
	marker := filepath.Join(root, "unexpected-command-write")
	return func(w http.ResponseWriter, _ *http.Request) {
		_, err := os.Stat(marker)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"allowed_origin": allowed.URL, "forbidden_origin": forbidden.URL,
			"allowed_requests": allowedHits.Load(), "forbidden_requests": forbiddenHits.Load(),
			"command_marker": marker, "command_executed": err == nil,
			"auth_mode": cfg.AuthMode,
		})
	}
}
