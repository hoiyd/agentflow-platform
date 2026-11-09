package skill

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentflow-platform/apps/api/internal/tool"
)

// Failure inventory: unreviewed/changed/revoked packages cannot freeze; missing,
// corrupt or ambiguous manifests fail closed; frozen text survives disk updates.
func TestReviewedPackageGateLifecycle(t *testing.T) {
	root := skillRoot(t, "report-method")
	manifest := filepath.Join(t.TempDir(), "reviews.json")
	approve := func(hash string) {
		t.Helper()
		data, _ := json.Marshal(map[string]any{"schema_version": "skill-reviews-v1", "approvals": []any{map[string]string{"name": "report-method", "hash": hash}}})
		if err := os.WriteFile(manifest, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	plain, err := LoadRoots([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	original := plain.List()[0].Hash
	approve(strings.Repeat("a", 64))
	catalog, err := LoadReviewedRoots([]string{root}, manifest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = catalog.Freeze([]string{"report-method"}, nil); err == nil {
		t.Fatal("unreviewed content admitted")
	}
	approve(original)
	frozen, err := catalog.Freeze([]string{"report-method"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "report-method", "SKILL.md")
	if err = os.WriteFile(file, []byte("---\nname: report-method\ndescription: Updated method.\n---\nUpdated instructions."), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = catalog.Freeze([]string{"report-method"}, nil); err == nil {
		t.Fatal("changed content inherited approval")
	}
	if err = ValidateFrozen(frozen); err != nil {
		t.Fatal("frozen protocol changed", err)
	}
	plain, err = LoadRoots([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	approve(plain.List()[0].Hash)
	updated, err := catalog.Freeze([]string{"report-method"}, nil)
	if err != nil || updated[0].Hash == original {
		t.Fatal(updated, err)
	}
	if err = os.WriteFile(manifest, []byte(`{"schema_version":"skill-reviews-v1","approvals":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = catalog.Freeze([]string{"report-method"}, nil); err == nil {
		t.Fatal("revocation ignored")
	}
	if catalog.List()[0].ReviewReason != "skill_review_required" {
		t.Fatal("missing readiness rejection")
	}
	if err = os.WriteFile(manifest, []byte(`broken`), 0600); err != nil {
		t.Fatal(err)
	}
	if catalog.List()[0].ReviewReason != "skill_review_manifest_invalid" {
		t.Fatal("manifest failure looked available")
	}
	if err = os.Remove(manifest); err != nil {
		t.Fatal(err)
	}
	if _, err = catalog.Freeze([]string{"report-method"}, nil); err == nil {
		t.Fatal("missing manifest admitted content")
	}
	if _, err = LoadReviewedRoots([]string{root}, ""); err != nil {
		t.Fatal("default compatibility lost", err)
	}
}

func TestReviewManifestRejectsInvalidInput(t *testing.T) {
	root := skillRoot(t, "report-method")
	path := filepath.Join(t.TempDir(), "reviews.json")
	for _, body := range []string{`{`, `null`, `{}`, `{"schema_version":"other","approvals":[]}`, `{"schema_version":"skill-reviews-v1","approvals":null}`, `{"schema_version":"skill-reviews-v1","approvals":[],"unknown":true}`, `{"schema_version":"skill-reviews-v1","approvals":[{"name":"report-method","hash":"bad"}]}`, `{"schema_version":"skill-reviews-v1","approvals":[]} {}`} {
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadReviewedRoots([]string{root}, path); err == nil {
			t.Fatalf("invalid manifest accepted: %s", body)
		}
	}
}

func TestReviewManifestRejectsAmbiguousOrUnprotectedFiles(t *testing.T) {
	root := skillRoot(t, "report-method")
	manifest := filepath.Join(t.TempDir(), "reviews.json")
	write := func(path, body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	valid := `{"schema_version":"skill-reviews-v1","approvals":[]}`
	for _, body := range []string{
		`{"schema_version":"skill-reviews-v1","approvals":[{"name":"report-method","hash":"` + strings.Repeat("a", 64) + `"},{"name":"report-method","hash":"` + strings.Repeat("b", 64) + `"}]}`,
		valid + strings.Repeat(" ", 65536),
	} {
		write(manifest, body)
		if _, err := LoadReviewedRoots([]string{root}, manifest); err == nil {
			t.Fatal("ambiguous or oversized manifest accepted")
		}
	}
	write(manifest, valid)
	link := filepath.Join(t.TempDir(), "linked.json")
	if err := os.Symlink(manifest, link); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadReviewedRoots([]string{root}, link); err == nil {
		t.Fatal("symlink manifest accepted")
	}
	inside := filepath.Join(root, "reviews.json")
	write(inside, valid)
	if _, err := LoadReviewedRoots([]string{root}, inside); err == nil {
		t.Fatal("package-root manifest accepted")
	}
	denied := false
	catalog, err := LoadReviewedRoots([]string{root}, manifest, func(string) error {
		if denied {
			return errors.New("operator file became model-writable")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	denied = true
	if _, err := catalog.Freeze([]string{"report-method"}, nil); err == nil {
		t.Fatal("live operator-file guard was skipped")
	}
}

func TestReviewedCatalogNewDuplicateAndMissingPackages(t *testing.T) {
	root := skillRoot(t, "report-method")
	manifest := filepath.Join(t.TempDir(), "reviews.json")
	if err := os.WriteFile(manifest, []byte(`{"schema_version":"skill-reviews-v1","approvals":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	catalog, err := LoadReviewedRoots([]string{root}, manifest)
	if err != nil {
		t.Fatal(err)
	}
	newRoot := skillRoot(t, "new-method")
	content, err := os.ReadFile(filepath.Join(newRoot, "new-method", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	add := func(dir string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, dir, "SKILL.md"), content, 0600); err != nil {
			t.Fatal(err)
		}
	}
	assertCode := func(name, code string) {
		t.Helper()
		_, err := catalog.Freeze([]string{name}, nil)
		var execution *tool.ExecutionError
		if !errors.As(err, &execution) || string(execution.Code) != code {
			t.Fatalf("want %s, got %v", code, err)
		}
	}
	add("new-method")
	assertCode("new-method", "skill_review_required")
	add("duplicate-method")
	assertCode("new-method", "skill_review_catalog_invalid")
	if err := os.RemoveAll(filepath.Join(root, "duplicate-method")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(root, "new-method")); err != nil {
		t.Fatal(err)
	}
	assertCode("new-method", "skill_unavailable")
}
