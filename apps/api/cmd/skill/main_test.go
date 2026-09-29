package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/skill/install/fallback"
)

func installProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, marker := range []string{"apps/api/go.mod", "apps/web/package.json"} {
		file := filepath.Join(root, marker)
		if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte("project marker"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(root)
	return root
}

func TestInstallUsesSharedProjectRoot(t *testing.T) {
	root := installProject(t)
	for _, directory := range []string{root, filepath.Join(root, "apps", "api")} {
		t.Chdir(directory)
		var out, stderr bytes.Buffer
		code := run(context.Background(), []string{"install"}, &out, &stderr, func(_ context.Context, opts fallback.Options) (fallback.Report, error) {
			if opts.Destination != filepath.Join(root, ".agents", "skills") {
				t.Fatalf("not the shared project directory: %s", opts.Destination)
			}
			return fallback.Report{}, nil
		})
		if code != 0 {
			t.Fatalf("working directory %s: %d %s", directory, code, stderr.String())
		}
	}
	t.Chdir(t.TempDir())
	var out, stderr bytes.Buffer
	if code := run(context.Background(), []string{"install"}, &out, &stderr, func(context.Context, fallback.Options) (fallback.Report, error) {
		t.Fatal("wrong working directory reached downloader")
		return fallback.Report{}, nil
	}); code != 1 || !strings.Contains(stderr.String(), "repository root") {
		t.Fatalf("missing working-directory check: %d %s", code, stderr.String())
	}
}

func TestInstallCommandOptionsAndOutput(t *testing.T) {
	installProject(t)
	for _, apply := range []bool{false, true} {
		args := []string{"install", "--repo", "example/writing", "--path", "skills/article-writing", "--ref", strings.Repeat("1", 40), "--dest", "/operator/downloads", "--timeout", "45s"}
		if apply {
			args = append(args, "--apply")
		}
		var out, stderr bytes.Buffer
		code := run(context.Background(), args, &out, &stderr, func(ctx context.Context, opts fallback.Options) (fallback.Report, error) {
			if opts.Repo != "example/writing" || opts.Path != "skills/article-writing" || opts.Destination != "/operator/downloads" || opts.Apply != apply || opts.Timeout != 45*time.Second {
				t.Fatalf("options: %+v", opts)
			}
			status := "preview"
			if apply {
				status = "installed"
			}
			return fallback.Report{Status: status, Commit: opts.Ref, Directory: "/operator/downloads/article-writing"}, nil
		})
		var report fallback.Report
		if code != 0 || json.Unmarshal(out.Bytes(), &report) != nil || report.TrustConfigurationChanged {
			t.Fatalf("output: code=%d %s %s", code, out.String(), stderr.String())
		}
		if !strings.Contains(stderr.String(), "TRUSTED_SKILL_DIRS") && apply {
			t.Fatal("missing explicit trust instructions")
		}
	}
}

type failedWriter struct{}

func (failedWriter) Write([]byte) (int, error) { return 0, errors.New("output unavailable") }

func TestInstallCommandFailures(t *testing.T) {
	installProject(t)
	for _, args := range [][]string{nil, {"unknown"}, {"install", "--unknown"}, {"install", "--timeout", "bad"}, {"install", "extra"}} {
		var out, stderr bytes.Buffer
		if code := run(context.Background(), args, &out, &stderr, func(context.Context, fallback.Options) (fallback.Report, error) {
			t.Fatal("unexpected installer invocation")
			return fallback.Report{}, nil
		}); code != 2 {
			t.Fatalf("args=%v code=%d", args, code)
		}
	}
	for _, failure := range []bool{false, true} {
		var stderr bytes.Buffer
		var out io.Writer = failedWriter{}
		if failure {
			out = &bytes.Buffer{}
		}
		code := run(context.Background(), []string{"install"}, out, &stderr, func(context.Context, fallback.Options) (fallback.Report, error) {
			if failure {
				return fallback.Report{}, errors.New("download denied")
			}
			return fallback.Report{Status: "installed"}, nil
		})
		if code != 1 || stderr.Len() == 0 {
			t.Fatalf("failure=%t code=%d stderr=%s", failure, code, stderr.String())
		}
	}
	var out, stderr bytes.Buffer
	if code := run(context.Background(), []string{"install", "--help"}, &out, &stderr, nil); code != 0 {
		t.Fatalf("help=%d", code)
	}
}
