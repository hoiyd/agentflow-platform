package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Each request passes through production HTTP and file access. Rejected input
// must not reach the provider or expose any of the rejected guidance contents.
func exerciseProjectGuidanceFaults(t *testing.T, call func(string, any) string, root, config, workspace, agent string, projects []any, providerCalls func() int) {
	t.Helper()
	file := filepath.Join(root, "alpha", "report-rules.md")
	original, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, path, project, code string
		change                    func()
	}{
		{name: "traversal", path: "../outside.md", code: "project_guidance_invalid"},
		{name: "pattern-instead-of-file", path: "reports/*.md", code: "project_guidance_invalid"},
		{name: "sensitive-input", path: ".env", code: "project_guidance_unavailable"},
		{name: "untrusted-root", project: "untrusted", code: "project_guidance_untrusted"},
		{name: "source-secret", code: "project_guidance_sensitive", change: func() {
			if err := os.WriteFile(file, []byte("Authorization: Bearer sk-abcdefghijklmnopqrstuvwxyz123456"), 0600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "file-byte-limit", code: "project_guidance_budget_exceeded", change: func() {
			if err := os.WriteFile(file, []byte(strings.Repeat("x", 16385)), 0600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "source-token-limit", code: "project_guidance_budget_exceeded", change: func() {
			if err := os.WriteFile(file, []byte(strings.Repeat("中", 4300)), 0600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "source-symlink", code: "project_guidance_unavailable", change: func() {
			if err := os.Remove(file); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(root, "beta", "report-rules.md"), file); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "missing-source", code: "project_guidance_unavailable", change: func() {
			if err := os.Remove(file); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "invalid-rule-is-not-global", code: "project_guidance_invalid", change: func() {
			writeHeadlessJSON(t, config, map[string]any{"projects": []any{map[string]any{"workspace_id": workspace, "root": "alpha", "entry": "GUIDE.md", "rules": []any{map[string]any{"path": "report-rules.md", "paths": []string{"reports/**"}}}}}})
		}},
		{name: "wrong-workspace-trust", code: "project_guidance_untrusted", change: func() {
			writeHeadlessJSON(t, config, map[string]any{"projects": []any{map[string]any{"workspace_id": "999999", "root": "alpha", "entry": "GUIDE.md"}}})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				_ = os.Remove(file)
				if err := os.WriteFile(file, original, 0600); err != nil {
					t.Error(err)
				}
				writeHeadlessJSON(t, config, map[string]any{"projects": projects})
			}()
			if tc.change != nil {
				tc.change()
			}
			name, target := tc.project, tc.path
			if name == "" {
				name = "alpha"
			}
			if target == "" {
				target = "reports/input.md"
			}
			before := providerCalls()
			response := call("/api/chat", map[string]any{"agent_id": agent, "message": "guidance-gate: reject unsafe project input", "project_context": map[string]any{"root": name, "paths": []string{target}}})
			if !strings.Contains(response, tc.code) || providerCalls() != before || strings.Contains(response, "abcdefghijklmnopqrstuvwxyz123456") {
				t.Fatalf("unsafe guidance outcome: %s", response)
			}
		})
	}
}
