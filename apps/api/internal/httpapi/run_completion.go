package httpapi

import (
	"context"
	"net/http"

	"agentflow-platform/apps/api/app/runcompletion"
	"agentflow-platform/apps/api/internal/domain"
)

type runCompletionRequest = runcompletion.Request

func (h *Handler) completionDependencies() runcompletion.Dependencies {
	return runcompletion.Dependencies{Runtime: h.agentRuntime, Verification: h.verification}
}

// Only transport owns flushing. Auxiliary memory work starts after terminal SSE.
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

func (h *Handler) freezeCompletionContract(contract *domain.CompletionContract) (*domain.CompletionContract, error) {
	return runcompletion.FreezeContract(h.verification, contract)
}

func writeTerminalRunDone(w http.ResponseWriter, flusher http.Flusher, run domain.Run) {
	writeSSE(w, "done", domain.ChatChunk{
		Type:               "done",
		ConversationID:     run.ConversationID,
		RunID:              run.ID,
		AgentID:            run.AgentID,
		Status:             string(run.Status),
		VerificationStatus: string(run.VerificationStatus),
	})
	flusher.Flush()
}
