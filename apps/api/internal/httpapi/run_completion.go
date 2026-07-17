package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/verification"
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

	completed, err := h.resolveRunCompletion(ctx, request.RunID, request.Assistant)
	if err != nil {
		_, _ = h.agentRuntime.FailRun(request.RunID, err)
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
		h.enqueueMemoryCuration(*request.UserMessage, request.RunID)
	}
	return true
}

func (h *Handler) freezeCompletionContract(contract *domain.CompletionContract) (*domain.CompletionContract, error) {
	if contract == nil {
		return nil, nil
	}
	if h.verification == nil {
		return nil, errors.New("verification engine is unavailable")
	}
	return h.verification.FreezeContract(contract)
}

func (h *Handler) resolveRunCompletion(ctx context.Context, runID, output string) (domain.Run, error) {
	run, ok, err := h.store.GetRun(runID)
	if err != nil {
		return domain.Run{}, err
	}
	if !ok {
		return domain.Run{}, errors.New("run not found")
	}
	// Verification is opt-in per Run. Server verifier configuration only makes
	// implementations available; it never changes an uncontracted chat Run.
	if run.CompletionContract == nil {
		return h.agentRuntime.CompleteRun(runID)
	}
	if h.verification == nil {
		return domain.Run{}, errors.New("verification engine is unavailable")
	}
	decision, err := h.verification.Verify(ctx, runID, verification.SubjectForRunOutput(output))
	if err != nil {
		_, _ = h.store.UpdateRunVerificationStatus(runID, domain.VerificationBlocked)
		return domain.Run{}, err
	}
	if decision.AllowCompletion {
		return h.agentRuntime.CompleteRun(runID)
	}
	return h.agentRuntime.RejectRunCompletion(runID, decision.RunStatus, decision.Summary)
}

func latestAssistantOutput(messages []domain.Message) string {
	for index := len(messages) - 1; index >= 0; index-- {
		if messages[index].Role == "assistant" && strings.TrimSpace(messages[index].Content) != "" {
			return messages[index].Content
		}
	}
	return ""
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
