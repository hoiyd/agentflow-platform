package app

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func checkProjectGuidanceDiscovery(t *testing.T, server, workspace, root string) {
	t.Helper()
	get := func(path string, status int) string {
		t.Helper()
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, server+path, nil)
		req.Header.Set("X-Workspace-ID", workspace)
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if !strings.Contains(response.Header.Get("Cache-Control"), "no-store") {
			t.Fatal("Workspace file discovery must not enter shared or stale caches")
		}
		body, _ := io.ReadAll(response.Body)
		if response.StatusCode != status || strings.Contains(string(body), root) {
			t.Fatalf("discovery status=%d body=%s", response.StatusCode, body)
		}
		return string(body)
	}
	catalog := get("/api/project-guidance/projects", 200)
	var listing struct {
		Configured bool `json:"configured"`
		Projects   []struct {
			Root string `json:"root"`
		} `json:"projects"`
	}
	if json.Unmarshal([]byte(catalog), &listing) != nil || !listing.Configured || len(listing.Projects) != 2 || listing.Projects[0].Root != "alpha" {
		t.Fatal("wrong scoped project catalog", catalog)
	}
	if err := os.WriteFile(filepath.Join(root, "alpha", ".env"), []byte("not readable"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "beta", "GUIDE.md"), filepath.Join(root, "alpha", "alias.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "alpha", "input[1].md"), []byte("Unsupported selection name."), 0600); err != nil {
		t.Fatal(err)
	}
	files := get("/api/project-guidance/projects/alpha/files", 200)
	if !strings.Contains(files, `"kind":"directory"`) || strings.Contains(files, ".env") || strings.Contains(files, "alias.md") || strings.Contains(files, "input[1].md") || strings.Contains(files, "subordinate to user") {
		t.Fatal("unsafe file metadata", files)
	}
	if files = get("/api/project-guidance/projects/alpha/files?path=reports", 200); !strings.Contains(files, "reports/input.md") {
		t.Fatal("cannot browse nested task files", files)
	}
	get("/api/project-guidance/projects/alpha/files?path=..", 400)
	get("/api/project-guidance/projects/alpha/files?path=.env", 400)
	get("/api/project-guidance/projects/alpha/files?path=alias.md", 400)
	get("/api/project-guidance/projects/alpha/files?path=report-rules.md", 400)
	get("/api/project-guidance/projects/invalid*/files", 400)
	get("/api/project-guidance/projects/foreign/files", 400)

	directory := filepath.Join(root, "alpha", "bulk")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	for index := range 501 {
		if err := os.WriteFile(filepath.Join(directory, fmt.Sprintf("input-%03d.md", index)), []byte("Synthetic material."), 0600); err != nil {
			t.Fatal(err)
		}
	}
	var bounded struct {
		Entries   []any `json:"entries"`
		Truncated bool  `json:"truncated"`
	}
	if json.Unmarshal([]byte(get("/api/project-guidance/projects/alpha/files?path=bulk", 200)), &bounded) != nil || len(bounded.Entries) != 500 || !bounded.Truncated {
		t.Fatal("directory listing did not retain its hard bound")
	}
	for _, tc := range []struct {
		name, file, content string
		status              int
	}{
		{"invalid-trust", "guidance.json", `{"unexpected":true}`, 400},
		{"missing-trust", "guidance.json", "", 400},
		{"revoked-trust", "guidance.json", `{"projects":[]}`, 200},
		{"revoked-roots", "roots.json", `{"roots":[]}`, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file := filepath.Join(root, tc.file)
			original, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := os.WriteFile(file, original, 0600); err != nil {
					t.Error(err)
				}
			}()
			if tc.content == "" {
				err = os.Remove(file)
			} else {
				err = os.WriteFile(file, []byte(tc.content), 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			catalog := get("/api/project-guidance/projects", tc.status)
			if tc.status == 200 && strings.Contains(catalog, `"root"`) {
				t.Fatal("revoked access still appeared in discovery", catalog)
			}
			get("/api/project-guidance/projects/alpha/files", 400)
		})
	}
}
