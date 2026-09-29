package skill

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func skillRoot(t *testing.T, names ...string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), ".agents", "skills")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		dir := packageDirectory(t, name, "name: "+name+"\ndescription: Write with evidence.", "Use supplied facts.")
		if err := os.Rename(dir, filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestLoadRootsDiscoversImmediatePackages(t *testing.T) {
	root := skillRoot(t, "brand-voice", "article-writing")
	for _, name := range []string{".skill-stage-unused", ".skill-install.lock", "not-a-package"} {
		if err := os.Mkdir(filepath.Join(root, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("operator notes"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "..", "..", "skills-lock.json"), []byte(`{"version":1}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"agents/openai.yaml", "scripts/write.sh", "install-receipt.json"} {
		file := filepath.Join(root, "article-writing", suffix)
		if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte("not runtime content"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	catalog, err := LoadRoots([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	direct, err := LoadDirectories([]string{filepath.Join(root, "brand-voice"), filepath.Join(root, "article-writing")})
	if err != nil || !reflect.DeepEqual(catalog.List(), direct.List()) {
		t.Fatalf("discovery changed package identity: %v", err)
	}
	frozen, err := catalog.Freeze([]string{"article-writing", "brand-voice"}, nil)
	if err != nil || ValidateFrozen(frozen) != nil || len(frozen) != 2 {
		t.Fatalf("discovered packages cannot freeze: %v", err)
	}
	for _, roots := range [][]string{nil, {skillRoot(t)}} {
		empty, err := LoadRoots(roots)
		if err != nil || len(empty.List()) != 0 {
			t.Fatalf("empty/disabled root: %v", err)
		}
	}
}

func TestLoadRootsFailureBoundaries(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing", ".agents", "skills")
	if _, err := LoadRoots([]string{missing}); err == nil || !strings.Contains(err.Error(), missing) || !strings.Contains(err.Error(), "working directory") {
		t.Fatalf("missing root lacks actionable error: %v", err)
	}
	root := skillRoot(t, "article-writing")
	if _, err := LoadRoots([]string{filepath.Join(root, "article-writing")}); err == nil || !strings.Contains(err.Error(), "parent") {
		t.Fatalf("exact package path silently treated as root: %v", err)
	}
	if _, err := LoadRoots([]string{root, root}); err == nil {
		t.Fatal("duplicate package names allowed")
	}
	if _, err := LoadRoots(make([]string, MaxSkills+1)); err == nil {
		t.Fatal("too many roots allowed")
	}
	for _, content := range []string{"invalid frontmatter", strings.Repeat("x", maxInstructionsBytes+4097)} {
		root := skillRoot(t, "invalid")
		if err := os.WriteFile(filepath.Join(root, "invalid", "SKILL.md"), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadRoots([]string{root}); err == nil {
			t.Fatal("invalid package was silently skipped")
		}
	}
	for _, target := range []string{t.TempDir(), filepath.Join(t.TempDir(), "missing")} {
		root := skillRoot(t)
		if err := os.Symlink(target, filepath.Join(root, "linked")); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadRoots([]string{root}); err == nil {
			t.Fatal("package symlink followed or silently skipped")
		}
	}
	for _, resource := range []string{"references/unsupported.pdf", "references/linked.md"} {
		root := skillRoot(t, "invalid")
		file := filepath.Join(root, "invalid", resource)
		if strings.HasSuffix(resource, ".pdf") {
			if err := os.WriteFile(file, []byte("binary"), 0600); err != nil {
				t.Fatal(err)
			}
		} else if err := os.Symlink("checklist.md", file); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadRoots([]string{root}); err == nil {
			t.Fatal("discovery bypassed resource boundary")
		}
	}
	names := []string{}
	for i := 0; i <= MaxSkills; i++ {
		names = append(names, "package-"+strings.Repeat("a", i+1))
	}
	if _, err := LoadRoots([]string{skillRoot(t, names...)}); err == nil {
		t.Fatal("package limit bypassed through discovery")
	}
}
