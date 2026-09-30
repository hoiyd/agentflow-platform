package skill

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestSkillCheckerMatchesLoaderAndDoesNotMutate(t *testing.T) {
	dir := packageDirectory(t, "writing", "name: writing\ndescription: Write evidence-based articles when asked.\nlicense: MIT\ncompatibility: Text-only runtime\nmetadata:\n  author: Example\n  agentflow-required-tools: knowledge_read", "Read [checklist](references/checklist.md). Ignore [remote](https://example.invalid/no-network) and `scripts/not-a-dependency.py`.")
	before, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	options := CheckOptions{Directories: []string{dir}, EffectiveTools: []string{"knowledge_read"}}
	report := Check(options)
	if !report.Compatible || len(report.Packages) != 1 || report.Packages[0].DependencyStatus != "available" {
		t.Fatalf("report=%+v", report)
	}
	catalog, err := LoadDirectories([]string{dir})
	if err != nil || report.Packages[0].PackageHash != catalog.List()[0].Hash {
		t.Fatalf("Loader identity mismatch: %v", err)
	}
	if !reflect.DeepEqual(report, Check(options)) {
		t.Fatal("report is not repeatable")
	}
	after, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if err != nil || string(before) != string(after) {
		t.Fatal("Skill check mutated package")
	}
	encoded, _ := json.Marshal(report)
	if strings.Contains(string(encoded), dir) || strings.Contains(string(encoded), "evidence-based articles") {
		t.Fatalf("private content leaked: %s", encoded)
	}
	t.Logf("skill_check_evidence schema=%s packages=%d compatible=%t hash=%s writes=false network=false", report.SchemaVersion, len(report.Packages), report.Compatible, report.Packages[0].PackageHash)
}

func TestSkillCheckerFailureInventory(t *testing.T) {
	for _, tc := range []struct{ name, header, body, code, status string }{
		{"yaml", "name: [", "body", "skill_invalid_package", "rejected"},
		{"duplicate-yaml", "name: duplicate-yaml\nname: duplicate-yaml\ndescription: useful", "body", "skill_invalid_package", "rejected"},
		{"missing", "", "Read [missing](references/missing.md).", "skill_resource_unavailable", "incompatible"},
		{"escape", "", "Read [private](../outside.md).", "skill_resource_denied", "incompatible"},
		{"encoded-escape", "", "Read [private](%2e%2e/outside.md).", "skill_resource_denied", "incompatible"},
		{"absolute", "", "Read [private](/etc/passwd).", "skill_resource_denied", "incompatible"},
		{"script-link", "", "Run [script](scripts/run.py).", "skill_resource_not_frozen", "incompatible"},
		{"allowed", "allowed-tools: Bash", "body", "skill_semantics_unsupported", "incompatible"},
		{"invocation", "disable-model-invocation: true", "body", "skill_semantics_unsupported", "incompatible"},
		{"unknown", "future-behavior: true", "body", "skill_semantics_unsupported", "incompatible"},
		{"arguments", "", "Write $ARGUMENTS[0] and $1.", "skill_arguments_unsupported", "incompatible"},
		{"dynamic", "", "Context: !`echo test`", "skill_command_unsupported", "incompatible"},
		{"secret", "", "sk-proj-" + strings.Repeat("a", 40), "skill_sensitive_content", "rejected"},
		{"oversize", "", strings.Repeat("x", maxInstructionsBytes+1), "skill_limit_exceeded", "rejected"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			header := "name: " + tc.name + "\ndescription: useful"
			if tc.header != "" {
				if strings.HasPrefix(tc.header, "name:") {
					header = tc.header
				} else {
					header += "\n" + tc.header
				}
			}
			dir := packageDirectory(t, tc.name, header, tc.body)
			if tc.name == "script-link" {
				if err := os.Mkdir(filepath.Join(dir, "scripts"), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "scripts/run.py"), []byte("never execute"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			report := Check(CheckOptions{Directories: []string{dir}})
			if report.Compatible || len(report.Packages) != 1 || report.Packages[0].Status != tc.status {
				t.Fatalf("report=%+v", report)
			}
			if !hasDiagnostic(report.Packages[0].Diagnostics, tc.code) {
				t.Fatalf("missing %s: %+v", tc.code, report)
			}
			if tc.status == "rejected" {
				_, err := LoadDirectories([]string{dir})
				if err == nil || loaderErrorCode(err) != tc.code {
					t.Fatalf("Loader and check disagree: %v", err)
				}
			}
			encoded, _ := json.Marshal(report)
			if strings.Contains(string(encoded), dir) || strings.Contains(string(encoded), "sk-proj-") || strings.Contains(string(encoded), "/etc/passwd") {
				t.Fatalf("report leaked sensitive input: %s", encoded)
			}
		})
	}
}

func TestSkillCheckerDiscoveryAndDependencies(t *testing.T) {
	root := skillRoot(t, "writing")
	if got := Check(CheckOptions{Roots: []string{root}}); !got.Compatible || len(got.Packages) != 1 {
		t.Fatalf("discovery=%+v", got)
	}
	if got := Check(CheckOptions{Roots: []string{root, root}}); got.Compatible || !hasDiagnostic(got.Packages[1].Diagnostics, "skill_name_conflict") {
		t.Fatalf("duplicate=%+v", got)
	}
	if got := Check(CheckOptions{Roots: []string{skillRoot(t)}}); !got.Compatible || len(got.Packages) != 0 {
		t.Fatalf("empty root=%+v", got)
	}
	for _, opts := range []CheckOptions{
		{}, {Roots: make([]string, MaxSkills+1)}, {Directories: make([]string, MaxSkills+1)},
		{Roots: []string{filepath.Join(t.TempDir(), "missing")}}, {Roots: []string{filepath.Join(root, "writing")}},
	} {
		if got := Check(opts); got.Compatible || len(got.Diagnostics) == 0 {
			t.Fatalf("invalid options=%+v", got)
		}
	}
	dir := packageDirectory(t, "dependent", "name: dependent\ndescription: useful\nmetadata:\n  agentflow-required-tools: knowledge_read", "body")
	for _, tools := range [][]string{nil, {}, {"knowledge_read"}} {
		got := Check(CheckOptions{Directories: []string{dir}, EffectiveTools: tools})
		want := "not_checked"
		if tools != nil {
			want = "missing"
			if len(tools) > 0 {
				want = "available"
			}
		}
		if got.Packages[0].DependencyStatus != want || got.Compatible != (want != "missing") {
			t.Fatalf("dependency=%+v", got)
		}
	}
}

func hasDiagnostic(items []Diagnostic, code string) bool {
	for _, item := range items {
		if item.Code == code {
			return true
		}
	}
	return false
}

func TestSkillCheckerResourceAndAuthoringBoundaries(t *testing.T) {
	dir := packageDirectory(t, "resources", "name: resources\ndescription: "+strings.Repeat("x", 513)+" anything", "[ref][local]\n\n[local]: references/checklist.md \"title\"\n\n# Local\n[anchor](#local)\n![remote](https://example.invalid/image.png)\n\n```md\n[not-a-link](references/does-not-exist.md)\n```\n")
	if err := os.WriteFile(filepath.Join(dir, "references", "checklist.md"), []byte("Read [other](./nested/guide.md#intro) and [missing](absent.md)."), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "references", "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "references", "nested", "guide.md"), []byte("Guidance"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "scripts"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "assets.csv"), []byte("unsupported root resource"), 0600); err != nil {
		t.Fatal(err)
	}
	report := Check(CheckOptions{Directories: []string{dir}})
	items := report.Packages[0].Diagnostics
	for _, code := range []string{"skill_description_long", "skill_description_broad", "skill_scripts_ignored", "skill_resource_unavailable"} {
		if !hasDiagnostic(items, code) {
			t.Fatalf("missing %s: %+v", code, items)
		}
	}
	missing := 0
	for _, issue := range items {
		if issue.Code == "skill_resource_unavailable" {
			missing++
			if issue.Path != "references/checklist.md" || issue.ResourcePath != "references/absent.md" {
				t.Fatalf("not actionable: %+v", issue)
			}
		}
	}
	if missing != 1 {
		t.Fatalf("fenced code/anchors caused false dependencies: %+v", items)
	}
	if err := os.WriteFile(filepath.Join(dir, "references", "absent.md"), []byte("Now supplied."), 0600); err != nil {
		t.Fatal(err)
	}
	if got := Check(CheckOptions{Directories: []string{dir}}); !got.Compatible {
		t.Fatalf("authoring advice blocks: %+v", got)
	}
	for _, link := range []string{"[license](LICENSE)", "![binary](image.png)", "[local](file:///etc/passwd)", "[local](file://localhost/etc/passwd)", "[bad](references/%zz.md)", "[backslash](references%5cescape.md)"} {
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: resources\ndescription: useful\n---\n"+link), 0600); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"LICENSE", "image.png"} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte("not read"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		if got := Check(CheckOptions{Directories: []string{dir}}); got.Compatible {
			t.Fatalf("unsupported link %s: %+v", link, got)
		}
	}
}

func TestSkillCheckerSymlinksLimitsAndPrivateNames(t *testing.T) {
	dir := packageDirectory(t, "linked", "name: linked\ndescription: useful", "body")
	link := filepath.Join(t.TempDir(), "linked")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	if got := Check(CheckOptions{Directories: []string{link}}); got.Compatible {
		t.Fatal("package symlink accepted")
	}
	if _, err := LoadDirectories([]string{link}); err == nil || loaderErrorCode(err) != "skill_invalid_resource" {
		t.Fatalf("Loader did not reject the same package link: %v", err)
	}
	if got := Check(CheckOptions{Directories: []string{link + string(os.PathSeparator)}}); got.Compatible {
		t.Fatal("trailing separator bypassed package link check")
	}
	if err := os.Symlink("checklist.md", filepath.Join(dir, "references", "link.md")); err != nil {
		t.Fatal(err)
	}
	got := Check(CheckOptions{Directories: []string{dir}})
	if got.Compatible || !hasDiagnostic(got.Packages[0].Diagnostics, "skill_invalid_resource") {
		t.Fatalf("resource link: %+v", got)
	}
	root := skillRoot(t)
	if err := os.Symlink(dir, filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	if got := Check(CheckOptions{Roots: []string{root}}); got.Compatible || !hasDiagnostic(got.Diagnostics, "skill_invalid_resource") {
		t.Fatalf("discovery link=%+v", got)
	}
	names := []string{}
	for i := 0; i <= MaxSkills; i++ {
		names = append(names, "package-"+strings.Repeat("a", i+1))
	}
	if got := Check(CheckOptions{Roots: []string{skillRoot(t, names...)}}); got.Compatible || !hasDiagnostic(got.Diagnostics, "skill_limit_exceeded") {
		t.Fatalf("unbounded discovery=%+v", got)
	}
	for _, name := range []string{"Wrong_Name", "sk-proj-" + strings.Repeat("a", 40)} {
		got := Check(CheckOptions{Directories: []string{filepath.Join(t.TempDir(), name)}})
		encoded, _ := json.Marshal(got)
		if got.Compatible || got.Packages[0].Name != "" || strings.Contains(string(encoded), name) {
			t.Fatalf("private name: %s", encoded)
		}
	}
	dir = packageDirectory(t, "many-links", "name: many-links\ndescription: useful", strings.Repeat("[a](references/checklist.md)\n", 65))
	if got := Check(CheckOptions{Directories: []string{dir}}); got.Compatible || !hasDiagnostic(got.Packages[0].Diagnostics, "skill_check_limit_exceeded") {
		t.Fatalf("link limit=%+v", got)
	}
}

func TestSkillCheckerBoundsUnknownExtensionsAndResourceFailures(t *testing.T) {
	header := "name: extension-limit\ndescription: useful"
	for i := 0; i < 80; i++ {
		header += "\nfuture-" + strings.Repeat("a", i+1) + ": enabled"
	}
	dir := packageDirectory(t, "extension-limit", header, "body")
	got := Check(CheckOptions{Directories: []string{dir}})
	if got.Compatible || len(got.Packages[0].Diagnostics) != 65 || !hasDiagnostic(got.Packages[0].Diagnostics, "skill_check_limit_exceeded") {
		t.Fatalf("unbounded report: %+v", got)
	}
	for _, tc := range []struct{ suffix, content, code string }{
		{"bad.md", "\xff\x00", "skill_invalid_package"},
		{"large.md", strings.Repeat("x", maxResourceBytes+1), "skill_limit_exceeded"},
		{"binary.pdf", "not supported", "skill_invalid_resource"},
		{"private.md", "sk-proj-" + strings.Repeat("a", 40), "skill_sensitive_content"},
	} {
		dir := packageDirectory(t, "resource-failure", "name: resource-failure\ndescription: useful", "body")
		if err := os.WriteFile(filepath.Join(dir, "references", tc.suffix), []byte(tc.content), 0600); err != nil {
			t.Fatal(err)
		}
		got := Check(CheckOptions{Directories: []string{dir}})
		_, err := LoadDirectories([]string{dir})
		if got.Compatible || !hasDiagnostic(got.Packages[0].Diagnostics, tc.code) || loaderErrorCode(err) != tc.code {
			t.Fatalf("resource mismatch: %+v %v", got, err)
		}
	}
}
