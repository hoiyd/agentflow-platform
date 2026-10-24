package projectguidance

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/failure"
)

type fixtureFiles struct {
	content map[string]string
	revoked bool
	reads   []string
}

func (f *fixtureFiles) ValidateGuidanceConfig(string) error { return nil }

func (f *fixtureFiles) ProjectRootRevision(_ context.Context, workspace, root string, paths []string) (string, error) {
	if f.revoked || workspace != "1" || root != "alpha" {
		return "", os.ErrPermission
	}
	return "root-identity", nil
}

func (f *fixtureFiles) ReadProjectText(_ context.Context, _, _, name string, _ int) (string, error) {
	f.reads = append(f.reads, name)
	text, ok := f.content[name]
	if !ok {
		return "", os.ErrNotExist
	}
	return text, nil
}

func guidanceFixture(t *testing.T, files *fixtureFiles, rules []map[string]any) (*Service, string) {
	t.Helper()
	name := filepath.Join(t.TempDir(), "guidance.json")
	writeGuidance(t, name, map[string]any{"projects": []any{map[string]any{"workspace_id": "1", "root": "alpha", "entry": "AGENTS.md", "rules": rules}}})
	service, err := New(name, files)
	if err != nil {
		t.Fatal(err)
	}
	return service, name
}

func writeGuidance(t *testing.T, name string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestGuidanceSelectionFreezeAndRevocation(t *testing.T) {
	files := &fixtureFiles{content: map[string]string{"AGENTS.md": "Use concise reports.", "rules/report.md": "Use ALPHA_REPORT headings.", "rules/duplicate.md": "Use ALPHA_REPORT headings."}}
	service, config := guidanceFixture(t, files, []map[string]any{
		{"path": "rules/report.md", "paths": []string{"reports/*.md"}},
		{"path": "rules/duplicate.md", "paths": []string{"reports/*.md"}},
		{"path": "rules/other.md", "paths": []string{"src/*.go"}},
	})
	selection := domain.ProjectContext{Root: "alpha", Paths: []string{"reports/input.md"}}
	frozen, err := service.Freeze(t.Context(), "1", selection)
	if err != nil {
		t.Fatal(err)
	}
	if len(frozen.Entries) != 4 || frozen.Entries[1].Hash == "" || frozen.Entries[1].Reason != "path_match" || frozen.Entries[2].Reason != "duplicate_content" || frozen.Entries[3].Reason != "path_not_matched" {
		t.Fatalf("selection=%+v", frozen)
	}
	if strings.Contains(strings.Join(files.reads, ","), "other.md") || frozen.Entries[2].Content != "" {
		t.Fatal("unmatched/duplicate instructions loaded")
	}
	files.content["rules/report.md"] = "Use ALPHA_REPORT_V2 headings."
	if err := service.Validate(t.Context(), "1", frozen); err != nil || frozen.Entries[1].Content != "Use ALPHA_REPORT headings." {
		t.Fatalf("old Run hot-loaded: %v %+v", err, frozen)
	}
	updated, err := service.Freeze(t.Context(), "1", selection)
	if err != nil || updated.Entries[1].Hash == frozen.Entries[1].Hash {
		t.Fatal("new Run did not capture updated guidance", err)
	}
	if err := service.Validate(t.Context(), "2", frozen); err == nil {
		t.Fatal("cross-Workspace frozen guidance accepted")
	}
	files.revoked = true
	if err := service.Validate(t.Context(), "1", frozen); err == nil {
		t.Fatal("revoked root accepted")
	}
	files.revoked = false
	writeGuidance(t, config, map[string]any{"projects": []any{}})
	if err := service.Validate(t.Context(), "1", frozen); err == nil {
		t.Fatal("removed trust record accepted")
	}
}

func TestGuidanceRejectsInvalidMetadataAndUnsafeContent(t *testing.T) {
	for _, pattern := range []string{"", "../*.md", "/reports/*", "reports/**", "reports/[", "reports\\*.md"} {
		t.Run(pattern, func(t *testing.T) {
			name := filepath.Join(t.TempDir(), "guidance.json")
			writeGuidance(t, name, map[string]any{"projects": []any{map[string]any{"workspace_id": "1", "root": "alpha", "entry": "AGENTS.md", "rules": []any{map[string]any{"path": "rules/report.md", "paths": []string{pattern}}}}}})
			if _, err := New(name, &fixtureFiles{}); err == nil {
				t.Fatal("invalid pattern accepted")
			}
		})
	}
	for _, tc := range []struct{ name, body, code string }{
		{"empty", "  ", "project_guidance_invalid"},
		{"secret", "Authorization: Bearer sk-abcdefghijklmnopqrstuvwxyz123456", "project_guidance_sensitive"},
		{"file-budget", strings.Repeat("x", 16385), "project_guidance_budget_exceeded"},
		{"token-budget", strings.Repeat("中", 4300), "project_guidance_budget_exceeded"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service, _ := guidanceFixture(t, &fixtureFiles{content: map[string]string{"AGENTS.md": tc.body}}, nil)
			_, err := service.Freeze(t.Context(), "1", domain.ProjectContext{Root: "alpha", Paths: []string{"reports/input.md"}})
			if err == nil || failure.Describe(err).Code != tc.code || strings.Contains(err.Error(), tc.body) {
				t.Fatalf("unsafe guidance result: %v", err)
			}
		})
	}
}
