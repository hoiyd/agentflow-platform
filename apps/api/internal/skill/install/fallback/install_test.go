package fallback

import (
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/skill"
)

const fixtureCommit = "1111111111111111111111111111111111111111"

// Keep production URLs intact while routing only the transport to a local fixture.
type fixtureTransport struct {
	client *http.Client
	base   string
}

func (f fixtureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	copy := req.Clone(req.Context())
	url := *req.URL
	copy.URL = &url
	base := strings.TrimPrefix(f.base, "http://")
	copy.URL.Scheme, copy.URL.Host = "http", base
	return f.client.Transport.RoundTrip(copy)
}

type githubFixture struct {
	trees    map[string]any
	blobs    map[string]string
	before   func(http.ResponseWriter, *http.Request) bool
	requests int
}

func blobID(content string) string {
	return fmt.Sprintf("%x", sha1.Sum([]byte(fmt.Sprintf("blob %d\x00%s", len(content), content))))
}

func newFixture() *githubFixture {
	body := "---\nname: article-writing\ndescription: Write with evidence.\n---\nUse only supplied facts."
	resource, license := "Check sources.", "MIT License\nCopyright contributors."
	f := &githubFixture{trees: map[string]any{}, blobs: map[string]string{}}
	entry := func(name, mode, kind, sha string, size int) map[string]any {
		return map[string]any{"path": name, "mode": mode, "type": kind, "sha": sha, "size": size}
	}
	tree := func(id string, entries ...map[string]any) {
		f.trees[id] = map[string]any{"sha": id, "tree": entries, "truncated": false}
	}
	tree(fixtureCommit, entry("skills", "040000", "tree", strings.Repeat("2", 40), 0), entry("LICENSE", "100644", "blob", blobID(license), len(license)))
	tree(strings.Repeat("2", 40), entry("article-writing", "040000", "tree", strings.Repeat("3", 40), 0))
	tree(strings.Repeat("3", 40), entry("SKILL.md", "100644", "blob", blobID(body), len(body)), entry("references", "040000", "tree", strings.Repeat("4", 40), 0), entry("scripts", "040000", "tree", strings.Repeat("5", 40), 0), entry("agents", "040000", "tree", strings.Repeat("6", 40), 0))
	tree(strings.Repeat("4", 40), entry("check.md", "100644", "blob", blobID(resource), len(resource)), entry("image.png", "100644", "blob", strings.Repeat("7", 40), 100))
	f.blobs[blobID(body)], f.blobs[blobID(resource)], f.blobs[blobID(license)] = body, resource, license
	return f
}

func (f *githubFixture) serve(w http.ResponseWriter, req *http.Request) {
	f.requests++
	if req.Header.Get("Authorization") != "" {
		http.Error(w, "credentials forwarded", 500)
		return
	}
	if f.before != nil && f.before(w, req) {
		return
	}
	w.Header().Set("Content-Type", "application/json")
	var value any
	switch {
	case strings.Contains(req.URL.Path, "/commits/"):
		value = map[string]any{"sha": fixtureCommit}
	case strings.Contains(req.URL.Path, "/git/trees/"):
		value = f.trees[filepath.Base(req.URL.Path)]
	case strings.Contains(req.URL.Path, "/git/blobs/"):
		id := filepath.Base(req.URL.Path)
		content, ok := f.blobs[id]
		if ok {
			value = map[string]any{"sha": id, "size": len(content), "encoding": "base64", "content": base64.StdEncoding.EncodeToString([]byte(content))}
		}
	}
	if value == nil {
		http.NotFound(w, req)
		return
	}
	_ = json.NewEncoder(w).Encode(value)
}

func fixtureClient(t *testing.T, fixture *githubFixture) *http.Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(fixture.serve))
	t.Cleanup(server.Close)
	return &http.Client{Transport: fixtureTransport{server.Client(), server.URL}}
}

func options(t *testing.T) Options {
	t.Helper()
	return Options{Repo: "example/writing", Path: "skills/article-writing", Ref: fixtureCommit, Destination: t.TempDir(), Timeout: time.Second}
}

func assertEmpty(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("destination changed: %v %v", entries, err)
	}
}

func TestPreviewAndInstallThroughProductionLoader(t *testing.T) {
	f := newFixture()
	client := fixtureClient(t, f)
	opts := options(t)
	opts.Ref = "main"
	preview, err := run(context.Background(), opts, client)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Status != "preview" || preview.Commit != fixtureCommit || preview.PackageHash == "" || len(preview.Files) != 3 || len(preview.Ignored) != 3 {
		t.Fatalf("preview: %+v", preview)
	}
	assertEmpty(t, opts.Destination)
	opts.Ref, opts.Apply = preview.Commit, true
	installed, err := run(context.Background(), opts, client)
	if err != nil {
		t.Fatal(err)
	}
	if installed.Status != "installed" || installed.PackageHash != preview.PackageHash || installed.FilesHash != preview.FilesHash {
		t.Fatalf("non-repeatable: %+v", installed)
	}
	if !strings.Contains(strings.Join(installed.Warnings, "\n"), "installation root") {
		t.Fatal("receipt does not explain installation-root trust")
	}
	discovered, err := skill.LoadRoots([]string{opts.Destination})
	if err != nil || len(discovered.List()) != 1 || discovered.List()[0].Hash != installed.PackageHash {
		t.Fatalf("fallback not discoverable through runtime root: %v", err)
	}
	catalog, err := skill.LoadDirectories([]string{installed.Directory})
	if err != nil || len(catalog.List()) != 1 || catalog.List()[0].Hash != installed.PackageHash {
		t.Fatalf("loader: %v %v", catalog, err)
	}
	data, err := os.ReadFile(filepath.Join(installed.Directory, "install-receipt.json"))
	var receipt Report
	if err != nil || json.Unmarshal(data, &receipt) != nil || receipt.Commit != fixtureCommit || receipt.PackageHash != installed.PackageHash {
		t.Fatalf("receipt: %s %v", data, err)
	}
	for _, file := range installed.Files {
		data, err := os.ReadFile(filepath.Join(installed.Directory, file.Path))
		if err != nil || len(data) != file.Bytes {
			t.Fatalf("file: %+v %v", file, err)
		}
		info, _ := os.Stat(filepath.Join(installed.Directory, file.Path))
		if info.Mode().Perm()&0111 != 0 {
			t.Fatal("executable installed")
		}
	}
	if _, err := os.Stat(filepath.Join(installed.Directory, "scripts")); !os.IsNotExist(err) {
		t.Fatal("scripts installed")
	}
	if _, err := os.Stat(filepath.Join(opts.Destination, ".env")); !os.IsNotExist(err) {
		t.Fatal("configuration modified")
	}
	if _, err := run(context.Background(), opts, client); err == nil {
		t.Fatal("existing package overwritten")
	}
	dataAfter, _ := os.ReadFile(filepath.Join(installed.Directory, "install-receipt.json"))
	if string(dataAfter) != string(data) {
		t.Fatal("existing receipt modified")
	}
	t.Logf("skill_install_evidence: commit=%s package_hash=%s files_hash=%s files=%d ignored=%d trust_changed=false", installed.Commit, installed.PackageHash, installed.FilesHash, len(installed.Files), len(installed.Ignored))
}

func TestInvalidOptionsDoNotDownload(t *testing.T) {
	for _, change := range []func(*Options){
		func(o *Options) { o.Repo = "https://github.com/x/y" }, func(o *Options) { o.Repo = "../repo" },
		func(o *Options) { o.Path = "../escape" }, func(o *Options) { o.Path = "/absolute" }, func(o *Options) { o.Path = "skills\\file" },
		func(o *Options) { o.Ref = "" }, func(o *Options) { o.Ref = "bad\nref" }, func(o *Options) { o.Apply = true; o.Ref = "main" },
		func(o *Options) { o.Timeout = 0 }, func(o *Options) { o.Timeout = 3 * time.Minute }, func(o *Options) { o.Destination = "" },
	} {
		f := newFixture()
		o := options(t)
		change(&o)
		if _, err := run(context.Background(), o, fixtureClient(t, f)); err == nil {
			t.Fatalf("accepted: %+v", o)
		}
		if f.requests != 0 {
			t.Fatal("invalid input caused network access")
		}
	}
}

func TestDownloadFailureDoesNotInstall(t *testing.T) {
	for _, tc := range []struct {
		name   string
		modify func(*githubFixture)
	}{
		{"missing", func(f *githubFixture) { delete(f.trees, strings.Repeat("3", 40)) }},
		{"truncated", func(f *githubFixture) { f.trees[strings.Repeat("3", 40)].(map[string]any)["truncated"] = true }},
		{"traversal", func(f *githubFixture) {
			f.trees[strings.Repeat("3", 40)].(map[string]any)["tree"] = []map[string]any{{"path": "../escape", "mode": "100644", "type": "blob", "sha": strings.Repeat("8", 40)}}
		}},
		{"symlink", func(f *githubFixture) {
			f.trees[strings.Repeat("3", 40)].(map[string]any)["tree"] = []map[string]any{{"path": "SKILL.md", "mode": "120000", "type": "blob", "sha": strings.Repeat("8", 40)}}
		}},
		{"submodule", func(f *githubFixture) {
			f.trees[strings.Repeat("3", 40)].(map[string]any)["tree"] = []map[string]any{{"path": "references", "mode": "160000", "type": "commit", "sha": strings.Repeat("8", 40)}}
		}},
		{"invalid frontmatter", func(f *githubFixture) {
			for id, body := range f.blobs {
				if strings.Contains(body, "name:") {
					f.blobs[id] = "no frontmatter"
				}
			}
		}},
		{"identity mismatch", func(f *githubFixture) {
			for id := range f.blobs {
				f.blobs[id] += "tampered"
			}
		}},
		{"429", func(f *githubFixture) {
			f.before = func(w http.ResponseWriter, r *http.Request) bool {
				http.Error(w, "secret error body", 429)
				return true
			}
		}},
		{"malformed JSON", func(f *githubFixture) {
			f.before = func(w http.ResponseWriter, r *http.Request) bool { fmt.Fprint(w, "{"); return true }
		}},
		{"redirect", func(f *githubFixture) {
			f.before = func(w http.ResponseWriter, r *http.Request) bool {
				http.Redirect(w, r, "https://evil.example/steal", 302)
				return true
			}
		}},
		{"response limit", func(f *githubFixture) {
			f.before = func(w http.ResponseWriter, r *http.Request) bool {
				fmt.Fprint(w, strings.Repeat("x", maxResponseBytes+1))
				return true
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture()
			tc.modify(f)
			o := options(t)
			o.Apply = true
			_, err := run(context.Background(), o, fixtureClient(t, f))
			if err == nil {
				t.Fatal("failure accepted")
			}
			if strings.Contains(err.Error(), "secret error body") {
				t.Fatal("remote error body leaked")
			}
			assertEmpty(t, o.Destination)
		})
	}
}

func TestCancelledAndMissingDestination(t *testing.T) {
	o := options(t)
	o.Apply = true
	f := newFixture()
	client := fixtureClient(t, f)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := run(ctx, o, client); err == nil {
		t.Fatal("cancelled install accepted")
	}
	assertEmpty(t, o.Destination)
	o.Destination = filepath.Join(o.Destination, "missing")
	if _, err := run(context.Background(), o, client); err == nil {
		t.Fatal("unconfigured destination accepted")
	}
}
