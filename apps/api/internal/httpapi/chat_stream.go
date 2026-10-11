package httpapi

import (
	"agentflow-platform/apps/api/internal/domain"
	"net/http"
)

func writeUnifiedRunEvent(w http.ResponseWriter, flusher http.Flusher, event domain.RunEvent) {
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
	writeUnifiedRunEvent(w, flusher, domain.RunEvent{Type: eventType, SchemaVersion: domain.CurrentRunEventSchemaVersion, ConversationID: conversationID, RunID: runID, Payload: map[string]any{"agent_id": agentID, "status": status}})
}

func writeSSE(w http.ResponseWriter, event string, value any) { _ = writeSSEFrame(w, 0, event, value) }
