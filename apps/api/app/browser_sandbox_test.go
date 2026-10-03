package app

import (
	"os"
	"path/filepath"
	"testing"

	"agentflow-platform/apps/api/internal/config"
	"agentflow-platform/apps/api/internal/tool"
	"agentflow-platform/apps/api/internal/tool/policy"
)

// The browser gate verifies production composition/persistence, not microVM
// isolation. A real sbx boundary has its own explicitly enabled live gate.
func browserSandboxConfig(t *testing.T, cfg *config.Config, root string) {
	t.Helper()
	cli := filepath.Join(root, "sbx-fixture")
	if err := os.WriteFile(cli, []byte("#!/bin/sh\ncase \"$1\" in create) if [ \"$2\" = shell ]; then printf '%s' '--skills --deny-network --cpus --memory --template'; fi;; exec) printf 'sandbox browser receipt';; esac\n"), 0700); err != nil {
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
}
