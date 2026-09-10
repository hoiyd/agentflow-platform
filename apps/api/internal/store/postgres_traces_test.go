package store

import (
	"strings"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
)

func TestPreparePostgresRunEventRedactsCredentialPayload(t *testing.T) {
	event, payload, err := preparePostgresRunEvent(domain.RunEvent{
		RunID: "run-1", Type: domain.EventRunCreated,
		Payload: map[string]any{"authorization": "Bearer private-token", "safe": "visible"},
	}, time.Now().UTC())
	if err != nil {
		t.Fatalf("prepare event: %v", err)
	}
	if event.Payload["authorization"] != "[REDACTED]" || strings.Contains(string(payload), "private-token") || !strings.Contains(string(payload), "visible") {
		t.Fatalf("credential leaked in event payload: event=%#v payload=%s", event, payload)
	}
}
