package verification

import (
	"context"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
)

func TestCommandVerifierRunsWithoutShellAndEnforcesBoundaries(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses standard Unix executables")
	}
	root := t.TempDir()
	registry := NewRegistry(Options{WorkspaceRoot: root, AllowedCommands: []string{"/usr/bin/printf", "/usr/bin/false"}, MaxArtifactBytes: 4})
	verifier, _ := registry.Resolve(domain.VerifierCommand)

	passed := verifier.Verify(context.Background(), domain.VerifierSpec{Command: &domain.CommandVerifierConfig{Args: []string{"/usr/bin/printf", "abcdef"}}}, Subject{})
	if passed.Status != domain.VerificationPassed || passed.Output != "abcd" || !passed.Truncated || passed.OutputBytes != 6 {
		t.Fatalf("unexpected command result: %#v", passed)
	}
	failed := verifier.Verify(context.Background(), domain.VerifierSpec{Command: &domain.CommandVerifierConfig{Args: []string{"/usr/bin/false"}}}, Subject{})
	if failed.Status != domain.VerificationFailed || failed.ExitCode == nil || *failed.ExitCode == 0 {
		t.Fatalf("expected command assertion failure, got %#v", failed)
	}
	blocked := verifier.Verify(context.Background(), domain.VerifierSpec{Command: &domain.CommandVerifierConfig{Args: []string{"rm", "-rf", "."}}}, Subject{})
	if blocked.Status != domain.VerificationBlocked {
		t.Fatalf("expected non-allowlisted command to be blocked, got %#v", blocked)
	}
}

func TestHTTPVerifierChecksStatusCapsOutputAndRestrictsHosts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("abcdef"))
	}))
	defer server.Close()
	registry := NewRegistry(Options{MaxArtifactBytes: 4})
	verifier, _ := registry.Resolve(domain.VerifierHTTP)

	passed := verifier.Verify(context.Background(), domain.VerifierSpec{HTTP: &domain.HTTPVerifierConfig{
		Method: http.MethodGet, URL: server.URL, ExpectedStatus: http.StatusAccepted,
	}}, Subject{})
	if passed.Status != domain.VerificationPassed || passed.Output != "abcd" || !passed.Truncated {
		t.Fatalf("unexpected http result: %#v", passed)
	}
	failed := verifier.Verify(context.Background(), domain.VerifierSpec{HTTP: &domain.HTTPVerifierConfig{
		Method: http.MethodGet, URL: server.URL, ExpectedStatus: http.StatusOK,
	}}, Subject{})
	if failed.Status != domain.VerificationFailed || !strings.Contains(failed.Summary, "got 202") {
		t.Fatalf("expected status mismatch, got %#v", failed)
	}
	blocked := verifier.Verify(context.Background(), domain.VerifierSpec{HTTP: &domain.HTTPVerifierConfig{
		Method: http.MethodGet, URL: "https://example.com", ExpectedStatus: http.StatusOK,
	}}, Subject{})
	if blocked.Status != domain.VerificationBlocked {
		t.Fatalf("expected external host to be blocked, got %#v", blocked)
	}
}

func TestJSONSchemaVerifierValidatesRunOutput(t *testing.T) {
	registry := NewRegistry(Options{})
	verifier, _ := registry.Resolve(domain.VerifierJSONSchema)
	spec := domain.VerifierSpec{JSONSchema: &domain.JSONSchemaVerifierConfig{Schema: map[string]any{
		"type":                 "object",
		"properties":           map[string]any{"status": map[string]any{"const": "ok"}},
		"required":             []any{"status"},
		"additionalProperties": false,
	}}}
	passed := verifier.Verify(context.Background(), spec, SubjectForRunOutput(`{"status":"ok"}`))
	if passed.Status != domain.VerificationPassed {
		t.Fatalf("expected schema match, got %#v", passed)
	}
	failed := verifier.Verify(context.Background(), spec, SubjectForRunOutput(`{"status":"no"}`))
	if failed.Status != domain.VerificationFailed || !strings.Contains(failed.Summary, "mismatch") {
		t.Fatalf("expected schema mismatch, got %#v", failed)
	}
}
