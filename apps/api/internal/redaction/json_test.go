package redaction

import (
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

func TestJSONFailsClosedForInvalidInput(t *testing.T) {
	if content, count, err := JSON([]byte(`{"broken"`)); err == nil || content != nil || count != 0 {
		t.Fatalf("invalid JSON was accepted: content=%q count=%d err=%v", content, count, err)
	}
}
