package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentflow-platform/apps/api/internal/skill"
)

func TestCheckCommandUsesRealLoaderOutsideProject(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	dir := filepath.Join(root, "writing")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "SKILL.md")
	data := []byte("---\nname: writing\ndescription: Useful\nmetadata:\n  agentflow-required-tools: knowledge_read\n---\nUse provided evidence.")
	if err := os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		args []string
		code int
	}{
		{[]string{"check", "--dir", dir, "--tools", "knowledge_read"}, 0},
		{[]string{"check", "--root", root}, 0},
		{[]string{"check", "--dir", dir, "--tools", ""}, 1},
		{[]string{"check", "--dir", dir, "--dir", dir}, 1},
	} {
		var out, stderr bytes.Buffer
		code := run(context.Background(), tc.args, &out, &stderr, nil)
		var report skill.CheckReport
		if code != tc.code || json.Unmarshal(out.Bytes(), &report) != nil || strings.Contains(out.String(), root) {
			t.Fatalf("code=%d stdout=%s stderr=%s", code, out.String(), stderr.String())
		}
		if report.SchemaVersion != "skill-check-v1" {
			t.Fatalf("unexpected Skill Checker report identifier: %s", report.SchemaVersion)
		}
		catalog, err := skill.LoadDirectories([]string{dir})
		if err != nil || report.Packages[0].PackageHash != catalog.List()[0].Hash {
			t.Fatalf("CLI diverged: %v", err)
		}
	}
	after, err := os.ReadFile(file)
	entries, readErr := os.ReadDir(root)
	if err != nil || readErr != nil || !bytes.Equal(data, after) || len(entries) != 1 {
		t.Fatal("read-only check changed filesystem")
	}
	for _, args := range [][]string{{"check"}, {"check", "--wat"}, {"check", "extra"}, {"check", "--dir"}} {
		var out, stderr bytes.Buffer
		if code := run(context.Background(), args, &out, &stderr, nil); code != 2 {
			t.Fatalf("syntax=%v code=%d", args, code)
		}
	}
	var out, stderr bytes.Buffer
	if code := run(context.Background(), []string{"check", "--help"}, &out, &stderr, nil); code != 0 {
		t.Fatalf("help=%d", code)
	}
	if code := run(context.Background(), []string{"check", "--dir", dir}, failedWriter{}, &stderr, nil); code != 1 {
		t.Fatalf("writer=%d", code)
	}
}
