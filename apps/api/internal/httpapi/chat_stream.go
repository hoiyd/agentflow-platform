package httpapi

import (
	"context"
	"net/http"
	"strings"

	"agentflow-platform/apps/api/internal/domain"
)

func writeUnifiedRunEvent(w http.ResponseWriter, flusher http.Flusher, event domain.RunEvent, assistant *strings.Builder) {
	if event.Type == domain.EventModelDelta && assistant != nil {
		if reset, _ := event.Payload["reset"].(bool); reset {
			assistant.Reset()
		}
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

// Stop returns a cancellation signal from execution, not an HTTP failure.
// Only a durably canceled Run can end normally; unsolicited cancellation and
// provider failures still use the existing failure contract.
func (h *Handler) finishRunStreamFailure(w http.ResponseWriter, flusher http.Flusher, r *http.Request, runID string, status int, err error, failRun bool) {
	run, ok, loadErr := h.scopedStore(r).GetRun(runID)
	if loadErr != nil {
		err, status = loadErr, http.StatusInternalServerError
	} else if ok && run.Status == domain.RunCanceled {
		writeTerminalRunDone(w, flusher, run)
		return
	} else if failRun {
		run, settleErr := h.agentRuntime.FailRun(runID, err)
		if settleErr != nil {
			err, status = settleErr, http.StatusInternalServerError
		} else if run.Status == domain.RunCanceled {
			writeTerminalRunDone(w, flusher, run)
			return
		}
	}
	writeSSE(w, "error", failureChatChunk(w, r, status, err))
	flusher.Flush()
}

// runExecutionContext keeps admitted work alive when its initiating HTTP
// connection closes. Explicit Run cancellation is bound inside Runtime.
func runExecutionContext(r *http.Request) context.Context {
	return context.WithoutCancel(r.Context())
}

func writeSSE(w http.ResponseWriter, event string, value any) {
	_ = writeSSEFrame(w, 0, event, value)
}
