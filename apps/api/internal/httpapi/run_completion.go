package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/verification"
)

type runCompletionRequest struct {
	WorkspaceID    string
	RunID          string
	ConversationID string
	UserInput      string
	Assistant      string
	UserMessage    *domain.Message
	GenerateTitle  bool
}

// completeStreamingRun centralizes the durable state transition shared by all
// streaming modes. The SSE response is flushed before asynchronous memory work.
func (h *Handler) completeStreamingRun(w http.ResponseWriter, flusher http.Flusher, r *http.Request, ctx context.Context, request runCompletionRequest) bool {
	scoped := h.scopedStoreForID(request.WorkspaceID)
	sources, citations, invalidCitationIDs, err := h.resolveRunCitations(scoped, request.RunID, request.Assistant)
	if err != nil {
		_, _ = h.agentRuntime.FailRun(request.RunID, err)
		writeSSE(w, "error", failureChatChunk(w, r, http.StatusInternalServerError, err))
		flusher.Flush()
		return false
	}
	message, err := scoped.AddMessageWithCitations(request.ConversationID, "assistant", request.Assistant, citations)
	if err != nil {
		_, _ = h.agentRuntime.FailRun(request.RunID, err)
		writeSSE(w, "error", failureChatChunk(w, r, http.StatusInternalServerError, err))
		flusher.Flush()
		return false
	}
	_, _ = scoped.CreateRunEvent(domain.RunEvent{
		Type: domain.EventCitationResolved, RunID: request.RunID, ConversationID: request.ConversationID,
		Payload: map[string]any{
			"protocol_version":     domain.RAGCitationProtocolVersion,
			"available_source_ids": citationSourceIDs(sources),
			"cited_source_ids":     citationSourceIDs(citations),
			"invalid_source_ids":   invalidCitationIDs,
			"message_id":           message.ID,
		},
	})

	completed, err := h.resolveRunCompletion(ctx, scoped, request.RunID, request.UserInput, request.Assistant)
	if err != nil {
		_, _ = h.agentRuntime.FailRun(request.RunID, err)
		writeSSE(w, "error", failureChatChunk(w, r, http.StatusInternalServerError, err))
		flusher.Flush()
		return false
	}

	title := ""
	if request.GenerateTitle {
		title = h.summarizeConversationTitleBestEffort(ctx, scoped, request.ConversationID, request.UserInput, request.Assistant)
	}
	writeSSE(w, "done", domain.ChatChunk{
		Type:               "done",
		ConversationID:     completed.ConversationID,
		Title:              title,
		RunID:              completed.ID,
		AgentID:            completed.AgentID,
		Status:             string(completed.Status),
		VerificationStatus: string(completed.VerificationStatus),
		MessageID:          message.ID,
		Citations:          citations,
		InvalidCitationIDs: invalidCitationIDs,
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

func (h *Handler) resolveRunCompletion(ctx context.Context, scoped store.WorkspaceStore, runID, question, output string) (domain.Run, error) {
	run, ok, err := scoped.GetRun(runID)
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
	if strings.TrimSpace(question) == "" {
		messages, listErr := scoped.ListMessages(run.ConversationID)
		if listErr != nil {
			return domain.Run{}, listErr
		}
		question = latestUserInput(messages)
	}
	decision, err := h.verification.Verify(ctx, runID, verification.SubjectForQuestionAnswer(question, output))
	if err != nil {
		_, _ = scoped.UpdateRunVerificationStatus(runID, domain.VerificationBlocked)
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

func latestUserInput(messages []domain.Message) string {
	for index := len(messages) - 1; index >= 0; index-- {
		if messages[index].Role == "user" && strings.TrimSpace(messages[index].Content) != "" {
			return messages[index].Content
		}
	}
	return ""
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
