package fallback

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestLoaderRejectionAndLicenseWarning(t *testing.T) {
	for _, body := range []string{"no frontmatter", "---\nname: wrong\ndescription: useful\n---\nbody", "---\nname: article-writing\ndescription: useful\n---\n" + strings.Repeat("x", 8193), "---\nname: article-writing\ndescription: useful\n---\n\xff", "---\nname: article-writing\ndescription: useful\n---\nsk-proj-" + strings.Repeat("a", 40)} {
		f := newFixture()
		item := f.trees[strings.Repeat("3", 40)].(map[string]any)["tree"].([]map[string]any)[0]
		id := blobID(body)
		f.blobs[id] = body
		item["sha"], item["size"] = id, len(body)
		o := options(t)
		o.Apply = true
		if _, err := run(context.Background(), o, fixtureClient(t, f)); err == nil {
			t.Fatalf("invalid body accepted: %q", body)
		}
		assertEmpty(t, o.Destination)
	}
	f := newFixture()
	root := f.trees[fixtureCommit].(map[string]any)
	root["tree"] = root["tree"].([]map[string]any)[:1]
	o := options(t)
	report, err := run(context.Background(), o, fixtureClient(t, f))
	if err != nil || !strings.Contains(strings.Join(report.Warnings, "\n"), "No package or repository License") {
		t.Fatalf("license warning: %+v %v", report, err)
	}
}

func TestConcurrentInstallPublishesOnePackage(t *testing.T) {
	o := options(t)
	o.Apply = true
	clients := []*http.Client{fixtureClient(t, newFixture()), fixtureClient(t, newFixture())}
	errors := make(chan error, 2)
	var workers sync.WaitGroup
	for _, client := range clients {
		workers.Add(1)
		go func() { defer workers.Done(); _, err := run(context.Background(), o, client); errors <- err }()
	}
	workers.Wait()
	close(errors)
	successes := 0
	for err := range errors {
		if err == nil {
			successes++
		}
	}
	entries, err := os.ReadDir(o.Destination)
	if successes != 1 || err != nil || len(entries) != 1 || entries[0].Name() != "article-writing" {
		t.Fatalf("successes=%d entries=%v err=%v", successes, entries, err)
	}
}

func TestNewFileAndPreviewTempFailures(t *testing.T) {
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := writeNew(root, "keep", []byte("original")); err != nil {
		t.Fatal(err)
	}
	if err := writeNew(root, "keep", []byte("replacement")); err == nil {
		t.Fatal("file overwritten")
	}
	data, _ := root.ReadFile("keep")
	if string(data) != "original" {
		t.Fatal("existing file changed")
	}
	t.Setenv("TMPDIR", filepath.Join(dir, "missing"))
	if _, err := stage(context.Background(), root, false, nil, Report{Name: "article-writing"}); err == nil {
		t.Fatal("preview temp failure ignored")
	}
}

func TestPublishPreservesExistingTargetsAndCleansFailures(t *testing.T) {
	for _, kind := range []string{"directory", "file", "symlink", "lock"} {
		t.Run(kind, func(t *testing.T) {
			o := options(t)
			o.Apply = true
			name := filepath.Join(o.Destination, "article-writing")
			switch kind {
			case "directory":
				if err := os.Mkdir(name, 0700); err != nil {
					t.Fatal(err)
				}
			case "file":
				if err := os.WriteFile(name, []byte("keep"), 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Symlink(t.TempDir(), name); err != nil {
					t.Fatal(err)
				}
			case "lock":
				if err := os.Mkdir(filepath.Join(o.Destination, ".skill-install.lock"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := run(context.Background(), o, fixtureClient(t, newFixture())); err == nil {
				t.Fatal("conflict accepted")
			}
			entries, _ := os.ReadDir(o.Destination)
			if len(entries) != 1 {
				t.Fatalf("partial install: %v", entries)
			}
		})
	}
	for _, kind := range []string{"cancelled", "invalid resource", "target race", "closed root"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			root, err := os.OpenRoot(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			report := Report{Name: "article-writing"}
			body := []byte("---\nname: article-writing\ndescription: useful\n---\nbody")
			contents := map[string][]byte{"SKILL.md": body}
			ctx := context.Background()
			switch kind {
			case "cancelled":
				c, cancel := context.WithCancel(ctx)
				cancel()
				ctx = c
			case "invalid resource":
				contents["references/a.md"] = []byte("\x00")
			case "target race":
				if err := root.Mkdir(report.Name, 0700); err != nil {
					t.Fatal(err)
				}
			case "closed root":
				_ = root.Close()
			}
			if _, err := stage(ctx, root, true, contents, report); err == nil {
				t.Fatal("publish failure ignored")
			}
			entries, _ := os.ReadDir(dir)
			expected := 0
			if kind == "target race" {
				expected = 1
			}
			if len(entries) != expected {
				t.Fatal(fmt.Sprint(entries))
			}
		})
	}
}
