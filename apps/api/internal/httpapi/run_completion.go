package httpapi

import (
	"context"
	"net/http"

	"agentflow-platform/apps/api/internal/domain"
)

type runCompletionRequest struct {
	RunID          string
	ConversationID string
	UserInput      string
	Assistant      string
	UserMessage    *domain.Message
	GenerateTitle  bool
}

// completeStreamingRun centralizes the durable state transition shared by all
// streaming modes. The SSE response is flushed before asynchronous memory work.
func (h *Handler) completeStreamingRun(w http.ResponseWriter, flusher http.Flusher, ctx context.Context, request runCompletionRequest) bool {
	message, err := h.store.AddMessage(request.ConversationID, "assistant", request.Assistant)
	if err != nil {
		_, _ = h.agentRuntime.FailRun(request.RunID, err)
		writeSSE(w, "error", domain.ChatChunk{Type: "error", Error: err.Error()})
		flusher.Flush()
		return false
	}

	completed, err := h.agentRuntime.CompleteRun(request.RunID)
	if err != nil {
		writeSSE(w, "error", domain.ChatChunk{Type: "error", Error: err.Error()})
		flusher.Flush()
		return false
	}

	title := ""
	if request.GenerateTitle {
		title = h.summarizeConversationTitleBestEffort(ctx, request.ConversationID, request.UserInput, request.Assistant)
	}
	writeSSE(w, "done", domain.ChatChunk{
		Type:           "done",
		ConversationID: completed.ConversationID,
		Title:          title,
		RunID:          completed.ID,
		AgentID:        completed.AgentID,
		Status:         string(completed.Status),
		MessageID:      message.ID,
	})
	flusher.Flush()

	if request.UserMessage != nil {
		h.enqueueMemorySync(*request.UserMessage, request.RunID)
	}
	h.enqueueMemorySync(message, request.RunID)
	return true
}

func writeTerminalRunDone(w http.ResponseWriter, flusher http.Flusher, run domain.Run) {
	writeSSE(w, "done", domain.ChatChunk{
		Type:           "done",
		ConversationID: run.ConversationID,
		RunID:          run.ID,
		AgentID:        run.AgentID,
		Status:         string(run.Status),
	})
	flusher.Flush()
}
