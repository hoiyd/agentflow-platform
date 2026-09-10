package redaction

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestJSONRemovesStructuredAndEmbeddedSecrets(t *testing.T) {
	redacted, count, err := JSON([]byte(`{"api_key":"sk-abcdefgh","nested":{"note":"Authorization: Bearer private-token"},"safe":"visible"}`))
	if err != nil {
		t.Fatal(err)
	}
	text := string(redacted)
	if count != 2 || strings.Contains(text, "abcdefgh") || strings.Contains(text, "private-token") || !strings.Contains(text, `"safe":"visible"`) {
		t.Fatalf("unexpected redaction: count=%d content=%s", count, text)
	}
}

func TestTextCoversProviderDatabaseAndHeaderCredentials(t *testing.T) {
	input := strings.Join([]string{
		"Bearer private-token", "Basic dXNlcjpwYXNz", "OPENAI_API_KEY=sk-abcdefgh",
		"token=actor-secret",
		"postgres://agent:database-password@localhost/agentflow",
		"eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjMifQ.signature",
		"gsk_abcdefgh12345678",
	}, " ")
	redacted, count := Text(input)
	for _, secret := range []string{"private-token", "dXNlcjpwYXNz", "sk-abcdefgh", "actor-secret", "database-password", "eyJhbGci", "gsk_abcdefgh"} {
		if strings.Contains(redacted, secret) {
			t.Fatalf("redacted text contains %q: %s", secret, redacted)
		}
	}
	if count != 7 {
		t.Fatalf("expected seven redactions, got %d: %s", count, redacted)
	}
}

func TestTextRedactsCompletePrivateKeyBlock(t *testing.T) {
	input := "-----BEGIN PRIVATE KEY-----\nYWJjZA==\n-----END PRIVATE KEY-----"
	redacted, count := Text(input)
	if count != 1 || redacted != "[REDACTED]" {
		t.Fatalf("private key was not fully redacted: count=%d content=%q", count, redacted)
	}
}

func TestValidationAndLogWriterFailClosedWithoutChangingSafeContent(t *testing.T) {
	if err := ValidateText("ordinary text about expired access tokens"); err != nil {
		t.Fatalf("safe prose was rejected: %v", err)
	}
	if err := ValidateValue(map[string]any{"nested": map[string]any{"api_key": "private"}}); !errors.Is(err, ErrCredentialContent) {
		t.Fatalf("structured credential was accepted: %v", err)
	}
	if err := ValidateText("Authorization: Bearer private-token"); !errors.Is(err, ErrCredentialContent) {
		t.Fatalf("credential text was accepted: %v", err)
	}

	var output bytes.Buffer
	logger := Writer{Writer: &output}
	input := []byte("request failed api_key=sk-abcdefgh\n")
	if count, err := logger.Write(input); err != nil || count != len(input) {
		t.Fatalf("write log: count=%d err=%v", count, err)
	}
	if strings.Contains(output.String(), "sk-abcdefgh") || !strings.Contains(output.String(), "[REDACTED]") {
		t.Fatalf("log was not redacted: %q", output.String())
	}
}

func TestAlreadyRedactedPlaceholderIsNotReportedAgain(t *testing.T) {
	redacted, count, err := JSON([]byte(`{"api_key":"[REDACTED]","message":"Bearer [REDACTED]"}`))
	if err != nil || count != 0 || string(redacted) != `{"api_key":"[REDACTED]","message":"Bearer [REDACTED]"}` {
		t.Fatalf("placeholder changed: content=%s count=%d err=%v", redacted, count, err)
	}
}

func TestJSONFailsClosedForInvalidInput(t *testing.T) {
	if content, count, err := JSON([]byte(`{"broken"`)); err == nil || content != nil || count != 0 {
		t.Fatalf("invalid JSON was accepted: content=%q count=%d err=%v", content, count, err)
	}
}
