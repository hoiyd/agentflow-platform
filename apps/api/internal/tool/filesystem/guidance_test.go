package filesystem

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProjectGuidanceUsesScopedNoFollowReader(t *testing.T) {
	f := newFixture(t)
	writeFixture(t, filepath.Join(f.root, "input.md"), "Project input.")
	writeFixture(t, filepath.Join(f.root, "AGENTS.md"), "Use local report headings.")
	revision, err := f.service.ProjectRootRevision(t.Context(), "1", "project", []string{"input.md"})
	if err != nil || revision == "" {
		t.Fatal("authorized project unavailable", err)
	}
	content, err := f.service.ReadProjectText(t.Context(), "1", "project", "AGENTS.md", 16384)
	if err != nil || content != "Use local report headings." {
		t.Fatal("guidance read failed", err)
	}
	if err := os.Symlink(filepath.Join(f.root, "input.md"), filepath.Join(f.root, "alias.md")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"../input.md", ".env", "alias.md", "missing.md", "."} {
		if _, err := f.service.ProjectRootRevision(t.Context(), "1", "project", []string{name}); err == nil {
			t.Fatalf("accepted unsafe project input %s", name)
		}
		if _, err := f.service.ReadProjectText(t.Context(), "1", "project", name, 16384); err == nil {
			t.Fatalf("accepted unsafe guidance %s", name)
		}
	}
	if _, err := f.service.ProjectRootRevision(t.Context(), "2", "project", nil); err == nil {
		t.Fatal("foreign Workspace granted")
	}
	writeFixture(t, f.config, `{"roots":[]}`)
	if _, err := f.service.ProjectRootRevision(t.Context(), "1", "project", nil); err == nil {
		t.Fatal("revoked project granted")
	}
}
