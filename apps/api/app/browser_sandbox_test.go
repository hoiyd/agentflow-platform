package app

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"agentflow-platform/apps/api/internal/config"
	"agentflow-platform/apps/api/internal/tool"
	"agentflow-platform/apps/api/internal/tool/policy"
)

// The browser gate verifies production composition/persistence, not microVM
// isolation. A real sbx boundary has its own explicitly enabled live gate.
func browserSandboxConfig(t *testing.T, cfg *config.Config, root string) http.Handler {
	t.Helper()
	cli := filepath.Join(root, "sbx-fixture")
	release := filepath.Join(root, "progress-release")
	script := fmt.Sprintf(`#!/bin/sh
case "$1" in
  create) if [ "$2" = shell ]; then printf '%%s' '--skills --deny-network --cpus --memory --template'; fi;;
  exec)
    case "$*" in *progress-wait*) while [ ! -f %q ]; do sleep 0.05; done;; esac
    printf 'sandbox browser receipt';;
esac
`, release)
	if err := os.WriteFile(cli, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	cfg.SandboxEnabled = true
	cfg.SandboxExecutable, cfg.SandboxStateDirectory = cli, filepath.Join(root, "sandboxes")
	tools := tool.DefaultConfig()
	tools.EnabledTools = append(tools.EnabledTools, "sandbox_command")
	tools.SecurityPolicy.Rules = append(tools.SecurityPolicy.Rules, policy.Rule{
		ID: "browser-sandbox", Tool: "sandbox_command", Action: policy.ActionAllowAndLog,
		Capability: tool.SandboxCommandTool(nil).Descriptor.Security,
	})
	if err := tool.SaveConfig(cfg.ToolConfigPath, tools); err != nil {
		t.Fatal(err)
	}
	controls := http.NewServeMux()
	controls.HandleFunc("POST /__fixture/tool-progress/block", func(w http.ResponseWriter, _ *http.Request) {
		if err := os.Remove(release); err != nil && !os.IsNotExist(err) {
			w.WriteHeader(500)
			return
		}
		w.WriteHeader(204)
	})
	controls.HandleFunc("POST /__fixture/tool-progress/release", func(w http.ResponseWriter, _ *http.Request) {
		if err := os.WriteFile(release, nil, 0600); err != nil {
			w.WriteHeader(500)
			return
		}
		w.WriteHeader(204)
	})
	return controls
}
