package fallback

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestGitHubTransportBoundaries(t *testing.T) {
	for _, raw := range []string{"http://api.github.com/file", "https://api.github.com:443/file", "https://user:secret@api.github.com/file", "https://api.github.com.evil.test/file", "https://127.0.0.1/file", "https://api.github.com/file#fragment"} {
		u, _ := url.Parse(raw)
		if checkURL(u) == nil {
			t.Fatalf("allowed %s", raw)
		}
	}
	for _, tc := range []struct {
		name  string
		setup func(*githubFixture, *github)
	}{
		{"request limit", func(f *githubFixture, g *github) { g.requests = maxRequests }},
		{"aggregate bytes", func(f *githubFixture, g *github) { g.bytes = maxDownloadBytes }},
		{"entry limit", func(f *githubFixture, g *github) { g.entries = maxEntries }},
		{"compression", func(f *githubFixture, g *github) {
			f.before = func(w http.ResponseWriter, r *http.Request) bool {
				w.Header().Set("Content-Encoding", "gzip")
				fmt.Fprint(w, "compressed")
				return true
			}
		}},
		{"invalid commit", func(f *githubFixture, g *github) {
			f.before = func(w http.ResponseWriter, r *http.Request) bool {
				_ = json.NewEncoder(w).Encode(map[string]any{"sha": "not-a-commit"})
				return true
			}
		}},
		{"redirect loop", func(f *githubFixture, g *github) {
			f.before = func(w http.ResponseWriter, r *http.Request) bool {
				http.Redirect(w, r, "https://api.github.com/again", 302)
				return true
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture()
			g := newGitHub("example/writing", fixtureClient(t, f))
			tc.setup(f, g)
			var err error
			if tc.name == "invalid commit" {
				_, err = g.commit(context.Background(), "main")
			} else {
				_, err = g.tree(context.Background(), fixtureCommit)
			}
			if err == nil {
				t.Fatal("boundary accepted")
			}
		})
	}
	if _, err := Run(context.Background(), Options{}); err == nil {
		t.Fatal("Run accepted missing options")
	}
}

func TestBlobAndChildIdentityFailures(t *testing.T) {
	for _, tc := range []struct {
		name     string
		response any
		item     treeEntry
	}{
		{"executable", nil, treeEntry{Mode: "100755", Type: "blob", SHA: strings.Repeat("8", 40)}},
		{"oversized", nil, treeEntry{Mode: "100644", Type: "blob", SHA: strings.Repeat("8", 40), Size: maxBlobBytes + 1}},
		{"metadata", map[string]any{"sha": "wrong"}, treeEntry{Mode: "100644", Type: "blob", SHA: strings.Repeat("8", 40)}},
		{"encoding", map[string]any{"sha": strings.Repeat("8", 40), "size": 0, "encoding": "utf-8"}, treeEntry{Mode: "100644", Type: "blob", SHA: strings.Repeat("8", 40)}},
		{"base64", map[string]any{"sha": strings.Repeat("8", 40), "size": 0, "encoding": "base64", "content": "!"}, treeEntry{Mode: "100644", Type: "blob", SHA: strings.Repeat("8", 40)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture()
			if tc.response != nil {
				f.before = func(w http.ResponseWriter, r *http.Request) bool {
					_ = json.NewEncoder(w).Encode(tc.response)
					return true
				}
			}
			g := newGitHub("example/writing", fixtureClient(t, f))
			if _, err := g.blob(context.Background(), tc.item); err == nil {
				t.Fatal("blob accepted")
			}
		})
	}
	f := newFixture()
	g := newGitHub("example/writing", fixtureClient(t, f))
	if _, err := g.child(context.Background(), treeEntry{Mode: "120000", Type: "blob"}); err == nil {
		t.Fatal("link followed")
	}
	if _, err := g.child(context.Background(), treeEntry{Mode: "040000", Type: "tree", SHA: fixtureCommit}); err != nil {
		t.Fatal(err)
	}
	f.trees[fixtureCommit].(map[string]any)["sha"] = strings.Repeat("9", 40)
	if _, err := g.child(context.Background(), treeEntry{Mode: "040000", Type: "tree", SHA: fixtureCommit}); err == nil {
		t.Fatal("mismatched tree accepted")
	}
}

func TestDeadlineCancelsInFlightDownload(t *testing.T) {
	f := newFixture()
	f.before = func(w http.ResponseWriter, r *http.Request) bool { <-r.Context().Done(); return true }
	o := options(t)
	o.Apply = true
	o.Timeout = 20 * time.Millisecond
	if _, err := run(context.Background(), o, fixtureClient(t, f)); err == nil {
		t.Fatal("deadline ignored")
	}
	assertEmpty(t, o.Destination)
}

func TestReadAndSourceFailures(t *testing.T) {
	for _, kind := range []string{"short response", "missing ref", "missing blob", "missing path", "unsafe license"} {
		t.Run(kind, func(t *testing.T) {
			f := newFixture()
			o := options(t)
			o.Apply = true
			switch kind {
			case "short response":
				f.before = func(w http.ResponseWriter, r *http.Request) bool {
					w.Header().Set("Content-Length", "1000")
					fmt.Fprint(w, "{")
					return true
				}
			case "missing ref":
				o.Apply, o.Ref = false, "missing"
				f.before = func(w http.ResponseWriter, r *http.Request) bool { http.NotFound(w, r); return true }
			case "missing blob":
				for id, body := range f.blobs {
					if body == "Check sources." {
						delete(f.blobs, id)
					}
				}
			case "missing path":
				o.Path = "skills/missing"
			case "unsafe license":
				f.trees[fixtureCommit].(map[string]any)["tree"].([]map[string]any)[1]["mode"] = "120000"
			}
			if _, err := run(context.Background(), o, fixtureClient(t, f)); err == nil {
				t.Fatal("source failure ignored")
			}
			assertEmpty(t, o.Destination)
		})
	}
}
