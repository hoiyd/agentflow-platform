package httpapi

import (
	"context"
	"net/http"

	"agentflow-platform/apps/api/app/runcompletion"
)

type runCompletionRequest = runcompletion.Request

// Completion-gate fixture adapter; production transport uses runcommands.
func (h *Handler) completeStreamingRun(w http.ResponseWriter, flusher http.Flusher, r *http.Request, ctx context.Context, request runCompletionRequest) bool {
	chunk, err := runcompletion.Complete(ctx, h.scopedStoreForID(request.WorkspaceID), h.completionDependencies(), request)
	if err != nil {
		writeSSE(w, "error", failureChatChunk(w, r, http.StatusInternalServerError, err))
		flusher.Flush()
		return false
	}
	writeSSE(w, "done", chunk)
	flusher.Flush()
	if request.UserMessage != nil {
		runcompletion.SyncMemoryTurn(h.memories, *request.UserMessage, request.RunID)
	}
	return true
}
