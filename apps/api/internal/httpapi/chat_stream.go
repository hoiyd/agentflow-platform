package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"agentflow-platform/apps/api/internal/domain"
)

func writeUnifiedRunEvent(w http.ResponseWriter, flusher http.Flusher, event domain.RunEvent, assistant *strings.Builder) {
	if event.Type == domain.EventModelDelta && assistant != nil {
		if delta, ok := event.Payload["delta"].(string); ok {
			assistant.WriteString(delta)
		}
	}
	writeSSE(w, string(event.Type), event)
	flusher.Flush()
}

func writeRunStateSSE(w http.ResponseWriter, flusher http.Flusher, conversationID, runID, agentID string, status domain.RunStatus) {
	eventType := domain.EventRunStarted
	if status == domain.RunWaitingForUser {
		eventType = domain.EventRunWaitingForUser
	}
	if status == domain.RunFailed || status == domain.RunFailedRecoverable {
		eventType = domain.EventRunFailed
	}
	writeUnifiedRunEvent(w, flusher, domain.RunEvent{Type: eventType, SchemaVersion: domain.CurrentRunEventSchemaVersion,
		ConversationID: conversationID, RunID: runID, Payload: map[string]any{"agent_id": agentID, "status": status}}, nil)
}

// runExecutionContext keeps admitted work alive when its initiating HTTP
// connection closes. Explicit Run cancellation is bound inside Runtime.
func runExecutionContext(r *http.Request) context.Context {
	return context.WithoutCancel(r.Context())
}

func writeSSE(w http.ResponseWriter, event string, value any) {
	bytes, _ := json.Marshal(value)
	_, _ = fmt.Fprintf(w, "event: %s\n", event)
	_, _ = fmt.Fprintf(w, "data: %s\n\n", string(bytes))
}
