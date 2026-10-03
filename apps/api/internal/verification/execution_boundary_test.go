package verification

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
)

func TestHTTPExecutionBoundary(t *testing.T) {
	var hits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte("verified"))
	}))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer redirect.Close()
	for _, tc := range []struct {
		name, method, url string
		allowed           []string
		passed            bool
	}{
		{name: "explicit origin", method: "GET", url: target.URL, allowed: []string{target.URL}, passed: true},
		{name: "default loopback denied", method: "GET", url: target.URL},
		{name: "different port denied", method: "GET", url: target.URL, allowed: []string{redirect.URL}},
		{name: "mutation denied", method: "POST", url: target.URL, allowed: []string{target.URL}},
		{name: "URL credentials denied", method: "GET", url: strings.Replace(target.URL, "://", "://user:password@", 1), allowed: []string{target.URL}},
		{name: "redirect denied", method: "GET", url: redirect.URL, allowed: []string{redirect.URL, target.URL}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := hits.Load()
			v, _ := NewRegistry(Options{AllowedHTTPHosts: tc.allowed}).Resolve(domain.VerifierHTTP)
			result := v.Verify(t.Context(), domain.VerifierSpec{Config: map[string]any{"method": tc.method, "url": tc.url, "expected_status": 200}}, Subject{})
			if tc.passed {
				if result.Status != domain.VerificationPassed || hits.Load() != before+1 {
					t.Fatalf("allowed request: %#v", result)
				}
			} else if result.Status != domain.VerificationBlocked || result.Details["reason_code"] != BlockedPolicyDenied || hits.Load() != before {
				t.Fatalf("boundary bypass: %#v, hits=%d", result, hits.Load()-before)
			}
		})
	}
}

func TestTrustedCommandEnvironmentAndDirectory(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("host supervision requires Linux or macOS")
	}
	t.Setenv("OPENAI_API_KEY", "boundary-fixture-secret")
	t.Setenv("OIDC_CLIENT_SECRET", "boundary-fixture-secret")
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	v, _ := NewRegistry(Options{AllowHostCommands: true, WorkspaceRoot: root, AllowedCommands: []string{"/usr/bin/env", "env"}}).Resolve(domain.VerifierCommand)
	spec := domain.VerifierSpec{Config: map[string]any{"args": []string{"/usr/bin/env"}}}
	result := v.Verify(t.Context(), spec, Subject{})
	if result.Status != domain.VerificationPassed || strings.Contains(result.Artifacts[0].Content, "boundary-fixture-secret") || strings.Contains(result.Artifacts[0].Content, "OPENAI_API_KEY") {
		t.Fatalf("inherited credentials: %#v", result)
	}
	for _, dir := range []string{"../", "escape", outside} {
		spec.Config["working_directory"] = dir
		result := v.Verify(t.Context(), spec, Subject{})
		if result.Status != domain.VerificationBlocked || result.Details["reason_code"] != BlockedPolicyDenied {
			t.Fatalf("cwd %s: %#v", dir, result)
		}
	}
	spec.Config = map[string]any{"args": []string{"env"}}
	if result := v.Verify(t.Context(), spec, Subject{}); result.Details["reason_code"] != BlockedPolicyDenied {
		t.Fatalf("PATH lookup enabled: %#v", result)
	}
}

func TestCommandCancellationStopsDescendants(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("host supervision requires Linux or macOS")
	}
	root := t.TempDir()
	marker := filepath.Join(root, "escaped-child")
	v, _ := NewRegistry(Options{AllowHostCommands: true, WorkspaceRoot: root, AllowedCommands: []string{"/bin/sh"}}).Resolve(domain.VerifierCommand)
	// A trusted fixture shell, not a product shell capability. A direct-child-only
	// cancellation would leave this background write alive after the verifier stops.
	ctx, cancel := context.WithTimeout(t.Context(), 80*time.Millisecond)
	defer cancel()
	result := v.Verify(ctx, domain.VerifierSpec{Config: map[string]any{"args": []string{"/bin/sh", "-c", `(sleep 0.4; printf escaped > "$1") & wait`, "fixture", marker}}}, Subject{})
	if result.Status != domain.VerificationBlocked || result.Details["reason_code"] != BlockedTimedOut {
		t.Fatalf("canceled process: %#v", result)
	}
	time.Sleep(500 * time.Millisecond)
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("descendant outlived canceled verifier: %v", err)
	}
}

func TestHTTPResponseLimitAndCancellation(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/wait" {
			<-r.Context().Done()
			return
		}
		_, _ = w.Write([]byte(strings.Repeat("x", (1<<20)+1)))
	}))
	defer s.Close()
	v, _ := NewRegistry(Options{AllowedHTTPHosts: []string{s.URL}, MaxArtifactBytes: 4}).Resolve(domain.VerifierHTTP)
	spec := domain.VerifierSpec{Config: map[string]any{"method": "GET", "url": s.URL, "expected_status": 200}}
	result := v.Verify(t.Context(), spec, Subject{})
	if result.Status != domain.VerificationBlocked || result.Details["reason_code"] != BlockedExecutionFailed || result.Artifacts[0].ByteSize > (1<<20)+1 {
		t.Fatalf("unbounded response: %#v", result)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	spec.Config["url"] = s.URL + "/wait"
	if result := v.Verify(ctx, spec, Subject{}); result.Details["reason_code"] != BlockedTimedOut {
		t.Fatalf("deadline: %#v", result)
	}
}

func TestBinaryHTTPArtifactIsSafeForPostgresText(t *testing.T) {
	content, changed := boundedArtifactContent("binary\x00value", 100)
	if !changed || strings.ContainsRune(content, 0) || content != "binary\uFFFDvalue" {
		t.Fatalf("artifact cannot be stored as Postgres text: %q changed=%v", content, changed)
	}
}

func TestHTTPBrokenBodyAndDeadlinesAreNotSuccess(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/broken" {
			w.Header().Set("Content-Length", "100")
			_, _ = w.Write([]byte("short"))
			return
		}
		if r.URL.Path == "/body-wait" {
			w.WriteHeader(200)
			w.(http.Flusher).Flush()
		}
		<-r.Context().Done()
	}))
	defer s.Close()
	for _, tc := range []struct {
		name, path string
		timeout    time.Duration
		reason     BlockedReason
	}{
		{"broken response", "/broken", time.Second, BlockedExecutionFailed},
		{"header deadline", "/headers-wait", 20 * time.Millisecond, BlockedTimedOut},
		{"body deadline", "/body-wait", 20 * time.Millisecond, BlockedTimedOut},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := newHTTPVerifier([]string{s.URL}, 4)
			v.client.Timeout = tc.timeout
			result := v.Verify(t.Context(), domain.VerifierSpec{Config: map[string]any{"method": "GET", "url": s.URL + tc.path, "expected_status": 200}}, Subject{})
			if result.Status != domain.VerificationBlocked || result.Details["reason_code"] != tc.reason {
				t.Fatalf("failure reported as success: %#v", result)
			}
		})
	}
	v := newHTTPVerifier([]string{s.URL}, 4)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if result := v.Verify(ctx, domain.VerifierSpec{Config: map[string]any{"method": "GET", "url": s.URL}}, Subject{}); result.Details["reason_code"] != BlockedCanceled {
		t.Fatalf("cancellation: %#v", result)
	}
}

func TestTrustedCommandMissingWorkspaceFailsClosed(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("host supervision requires Linux or macOS")
	}
	for _, root := range []string{"", filepath.Join(t.TempDir(), "missing")} {
		v, _ := NewRegistry(Options{AllowHostCommands: true, WorkspaceRoot: root, AllowedCommands: []string{"/usr/bin/printf"}}).Resolve(domain.VerifierCommand)
		result := v.Verify(t.Context(), domain.VerifierSpec{Config: map[string]any{"args": []string{"/usr/bin/printf", "should-not-run"}}}, Subject{})
		if result.Status != domain.VerificationBlocked || result.Details["reason_code"] != BlockedConfigInvalid {
			t.Fatalf("missing root: %#v", result)
		}
	}
}

func TestCommandDeniedUnlessTrustedLocal(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(root, "should-not-exist")
	v, _ := NewRegistry(Options{WorkspaceRoot: root, AllowedCommands: []string{"/usr/bin/touch"}}).Resolve(domain.VerifierCommand)
	result := v.Verify(context.Background(), domain.VerifierSpec{Config: map[string]any{"args": []string{"/usr/bin/touch", marker}}}, Subject{})
	if result.Status != domain.VerificationBlocked || result.Details["reason_code"] != BlockedPolicyDenied {
		t.Fatalf("untrusted host execution: %#v", result)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("unexpected command side effect: %v", err)
	}
}
