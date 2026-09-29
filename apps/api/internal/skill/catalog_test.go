package skill

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
)

func packageDirectory(t *testing.T, name, header, body string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(filepath.Join(dir, "references"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\n"+header+"\n---\n"+body), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "references", "checklist.md"), []byte("Check delivered evidence, not only source titles."), 0600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestTrustedCatalogFailureLimitsAndUTF8Pages(t *testing.T) {
	if got := (*Catalog)(nil).List(); len(got) != 0 {
		t.Fatal("nil catalog is not empty")
	}
	if _, err := (*Catalog)(nil).Freeze([]string{"unknown"}, nil); err == nil {
		t.Fatal("nil catalog granted Skill")
	}
	if _, err := LoadDirectories(make([]string, MaxSkills+1)); err == nil {
		t.Fatal("catalog limit ignored")
	}
	if _, err := LoadDirectories([]string{filepath.Join(t.TempDir(), "missing")}); err == nil {
		t.Fatal("missing directory trusted")
	}
	for _, tc := range []struct{ name, file string }{
		{"plain", "no frontmatter"}, {"unclosed", "---\nname: unclosed\ndescription: bad"},
		{"multi", "---\nname: multi\ndescription: bad\n...\nname: other\n---\nbody"},
		{"malformed", "---\nname: [\n---\nbody"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := packageDirectory(t, tc.name, "name: "+tc.name+"\ndescription: useful", "body")
			if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(tc.file), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadDirectories([]string{dir}); err == nil {
				t.Fatal("invalid frontmatter trusted")
			}
		})
	}
	for _, tc := range []struct {
		name            string
		resources       int
		content, suffix string
	}{
		{"binary", 1, "\xff\x00", ".md"}, {"oversize", 1, strings.Repeat("x", maxResourceBytes+1), ".md"},
		{"count", maxResources + 1, "text", ".md"}, {"package", 5, strings.Repeat("x", maxResourceBytes), ".md"},
		{"script", 1, "print('run')", ".py"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := packageDirectory(t, tc.name, "name: "+tc.name+"\ndescription: useful", "body")
			for i := 0; i < tc.resources; i++ {
				if err := os.WriteFile(filepath.Join(dir, "references", fmt.Sprint(i)+tc.suffix), []byte(tc.content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := LoadDirectories([]string{dir}); err == nil {
				t.Fatal("resource limit ignored")
			}
		})
	}
	item := domain.SkillSnapshot{Name: "utf8", Description: "useful", Instructions: "body", Resources: []domain.SkillResource{{Path: "references/words.md", Content: "你好x"}}}
	item.Resources[0].Hash = contentHash(item.Resources[0].Content)
	item.Hash = packageHash(item)
	for _, page := range []struct{ offset, limit int }{{1, 3}, {0, 1}, {7, 4}, {0, MaxPageBytes + 1}} {
		if _, err := ReadResource(item, item.Resources[0].Path, page.offset, page.limit); err == nil {
			t.Fatalf("invalid UTF8 page=%v", page)
		}
	}
	if got, err := ReadResource(item, item.Resources[0].Path, 0, 4); err != nil || got.Content != "你" || got.NextOffset != 3 {
		t.Fatalf("page=%#v err=%v", got, err)
	}
	if got, err := ReadResource(item, item.Resources[0].Path, 3, 4); err != nil || got.Content != "好x" || got.Truncated {
		t.Fatalf("end=%#v err=%v", got, err)
	}
	if _, err := ReadResource(item, "references/missing.md", 0, 4); err == nil {
		t.Fatal("missing resource accepted")
	}
	item.Instructions = "changed"
	if _, err := ReadResource(item, item.Resources[0].Path, 0, 4); err == nil {
		t.Fatal("changed package read")
	}
	if SupportedResourcePath("references/" + strings.Repeat("a", 260) + ".md") {
		t.Fatal("unaddressable long resource path accepted")
	}
}

func TestFrozenPackageValidationFailures(t *testing.T) {
	base := domain.SkillSnapshot{Name: "method", Description: "Useful", Instructions: "Body", Resources: []domain.SkillResource{{Path: "references/a.md", Content: "facts", Hash: contentHash("facts")}}}
	base.Hash = packageHash(base)
	if err := ValidateFrozen(make([]domain.SkillSnapshot, MaxSkills+1)); err == nil {
		t.Fatal("snapshot limit ignored")
	}
	for _, modify := range []func(*domain.SkillSnapshot){
		func(i *domain.SkillSnapshot) { i.Name = "INVALID" }, func(i *domain.SkillSnapshot) { i.Description = strings.Repeat("x", 1025) },
		func(i *domain.SkillSnapshot) { i.Instructions = "\x00" }, func(i *domain.SkillSnapshot) { i.Instructions = strings.Repeat("x", maxInstructionsBytes+1) },
		func(i *domain.SkillSnapshot) { i.Description = "sk-proj-" + strings.Repeat("a", 40) },
		func(i *domain.SkillSnapshot) { i.Resources = append(i.Resources, i.Resources[0]) },
		func(i *domain.SkillSnapshot) { i.Resources[0].Path = "../escape.md" },
		func(i *domain.SkillSnapshot) {
			i.Resources[0].Content = "\xff"
			i.Resources[0].Hash = contentHash("\xff")
		},
	} {
		item := base
		item.Resources = append([]domain.SkillResource(nil), base.Resources...)
		modify(&item)
		item.Hash = packageHash(item)
		if err := ValidateFrozen([]domain.SkillSnapshot{item}); err == nil {
			t.Fatalf("invalid snapshot accepted: %#v", item)
		}
	}
	if err := ValidateFrozen([]domain.SkillSnapshot{base, base}); err == nil {
		t.Fatal("duplicate identity accepted")
	}
	if _, err := Bound(nil, "agent"); err == nil {
		t.Fatal("absent snapshot accepted")
	}
	if _, err := Bound(&domain.RuntimeSnapshot{Agent: domain.RuntimeAgentSnapshot{ID: "agent", Skills: []string{"missing"}}}, "agent"); err == nil {
		t.Fatal("missing binding body accepted")
	}
}

func TestTrustedCatalogFreezeAndValidation(t *testing.T) {
	dir := packageDirectory(t, "evidence-research", "name: evidence-research\ndescription: >-\n  Research questions with\n  traceable evidence.\nmetadata:\n  agentflow-required-tools: knowledge_search knowledge_read\nallowed-tools: dangerous_shell", "Read [checklist](references/checklist.md).")
	catalog, err := LoadDirectories([]string{dir})
	if err != nil {
		t.Fatal(err)
	}
	if got := catalog.List(); len(got) != 1 || got[0].Name != "evidence-research" || !strings.Contains(got[0].Description, "traceable evidence") {
		t.Fatalf("metadata=%#v", got)
	}
	frozen, err := catalog.Freeze([]string{"evidence-research", "evidence-research"}, []string{"knowledge_search", "knowledge_read"})
	if err != nil || len(frozen) != 1 || len(frozen[0].Resources) != 1 {
		t.Fatalf("frozen=%#v err=%v", frozen, err)
	}
	if err := ValidateFrozen(frozen); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.Freeze([]string{"evidence-research"}, nil); err == nil {
		t.Fatal("missing dependency accepted")
	}
	if _, err := catalog.Freeze([]string{"unknown"}, nil); err == nil {
		t.Fatal("untrusted name accepted")
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(frozen[0].Instructions, "checklist") {
		t.Fatal("frozen body changed")
	}
	frozen[0].Instructions = "tampered"
	if err := ValidateFrozen(frozen); err == nil {
		t.Fatal("invalid frozen hash accepted")
	}
}

func TestTrustedCatalogRejectsUnsafePackages(t *testing.T) {
	for _, tc := range []struct{ name, header, body string }{
		{"bad-name", "name: Wrong_Name\ndescription: useful", "body"},
		{"missing", "name: missing", "body"},
		{"duplicate", "name: duplicate\nname: duplicate\ndescription: useful", "body"},
		{"alias", "name: alias\ndescription: &x useful\nmetadata: *x", "body"},
		{"empty", "name: empty\ndescription: useful", ""},
		{"large", "name: large\ndescription: useful", strings.Repeat("x", 20000)},
		{"secret", "name: secret\ndescription: " + "sk-proj-" + strings.Repeat("a", 40), "body"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := packageDirectory(t, tc.name, tc.header, tc.body)
			if _, err := LoadDirectories([]string{dir}); err == nil {
				t.Fatal("invalid package accepted")
			}
		})
	}
	one := packageDirectory(t, "same", "name: same\ndescription: useful", "body")
	two := packageDirectory(t, "same", "name: same\ndescription: useful", "different")
	if _, err := LoadDirectories([]string{one, two}); err == nil {
		t.Fatal("name collision accepted")
	}
	outside := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(outside, []byte("OUTSIDE_SECRET"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(one, "references", "escape.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadDirectories([]string{one}); err == nil {
		t.Fatal("symlink accepted")
	}
}

func TestFrozenResourcesRejectEscapeAndDrift(t *testing.T) {
	dir := packageDirectory(t, "bounded", "name: bounded\ndescription: useful", "body")
	catalog, err := LoadDirectories([]string{dir})
	if err != nil {
		t.Fatal(err)
	}
	frozen, err := catalog.Freeze([]string{"bounded"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/etc/passwd", "../SKILL.md", "references/../../secret", "references\\escape.md", "scripts/run.sh"} {
		if _, err := ReadResource(frozen[0], path, 0, 512); err == nil {
			t.Fatalf("unsafe path accepted: %q", path)
		}
	}
	page, err := ReadResource(frozen[0], "references/checklist.md", 0, 12)
	if err != nil || page.NextOffset != 12 || !page.Truncated || page.Hash == "" {
		t.Fatalf("page=%#v err=%v", page, err)
	}
	if _, err := ReadResource(frozen[0], "references/checklist.md", -1, 12); err == nil {
		t.Fatal("negative offset accepted")
	}
	if _, err := ReadResource(frozen[0], "references/checklist.md", 0, 0); err == nil {
		t.Fatal("zero limit accepted")
	}
	frozen[0].Resources[0].Content = "tampered"
	if err := ValidateFrozen(frozen); err == nil {
		t.Fatal("resource drift accepted")
	}
}
