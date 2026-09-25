package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"

	agentpkg "agentflow-platform/apps/api/internal/agent"
	"agentflow-platform/apps/api/internal/apicontract"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/store"
)

func (h *Handler) chat(w http.ResponseWriter, r *http.Request) {
	var input apicontract.ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	req := chatRequestFromContract(input)
	workspaceID, matches := resolvePayloadWorkspace(r, req.WorkspaceID)
	if !matches {
		writeError(w, http.StatusBadRequest, "workspace_id does not match request scope")
		return
	}
	req.WorkspaceID = workspaceID
	if rejectCredentialContent(w, r, req) {
		return
	}
	scoped := h.scopedStoreForID(workspaceID)
	req.Message = strings.TrimSpace(req.Message)
	if req.Message == "" {
		writeError(w, http.StatusBadRequest, "message is required")
		return
	}
	contract, err := h.freezeCompletionContract(req.CompletionContract)
	if err != nil {
		writeFailure(w, r, http.StatusBadRequest, err)
		return
	}
	req.CompletionContract = contract
	reservation, ok := h.reserveRunCapacity(w, r)
	if !ok {
		return
	}
	defer reservation.Cancel()

	conversationID := strings.TrimSpace(req.ConversationID)
	if conversationID == "" {
		conversation, err := scoped.CreateConversation(req.Message)
		if err != nil {
			writeFailure(w, r, http.StatusInternalServerError, err)
			return
		}
		conversationID = conversation.ID
	} else if _, ok, err := scoped.GetConversation(conversationID); err != nil {
		writeFailure(w, r, http.StatusInternalServerError, err)
		return
	} else if !ok {
		writeError(w, http.StatusNotFound, "conversation not found")
		return
	}
	releaseRun, ok := h.acquireRunSlot(w, r, reservation, conversationID)
	if !ok {
		return
	}
	defer releaseRun()

	userMessage, err := scoped.AddMessage(conversationID, "user", req.Message)
	if err != nil {
		writeFailure(w, r, http.StatusInternalServerError, err)
		return
	}

	history, err := scoped.ListMessages(conversationID)
	if err != nil {
		writeFailure(w, r, http.StatusInternalServerError, err)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming is not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	writeSSE(w, "conversation", domain.ChatChunk{Type: "conversation", ConversationID: conversationID})
	flusher.Flush()

	mode := agentpkg.NormalizeChatMode(req.Mode)
	if mode == agentpkg.ChatModeAutonomous {
		h.chatAutonomous(w, flusher, r, req, conversationID, userMessage)
		return
	}
	if mode == agentpkg.ChatModeMultiAgent {
		h.chatMultiAgent(w, flusher, r, req, conversationID, userMessage)
		return
	}

	runCtx := runExecutionContext(r)
	prepared, err := h.agentRuntime.PrepareChatRunWithContract(runCtx, req.AgentID, conversationID, req.CompletionContract)
	if err != nil {
		status := http.StatusInternalServerError
		if store.IsNotFound(err) {
			status = http.StatusNotFound
		}
		writeSSE(w, "error", failureChatChunk(w, r, status, err))
		flusher.Flush()
		return
	}
	writeRunStateSSE(w, flusher, conversationID, prepared.Run.ID, prepared.Agent.ID, prepared.Run.Status)

	events, errs := h.agentRuntime.StreamChat(runCtx, prepared, history, req.Message)
	var assistant strings.Builder
	for event := range events {
		writeUnifiedRunEvent(w, flusher, event, &assistant)
	}

	if err := <-errs; err != nil {
		_, _ = h.agentRuntime.FailRun(prepared.Run.ID, err)
		writeSSE(w, "error", failureChatChunk(w, r, http.StatusInternalServerError, err))
		flusher.Flush()
		return
	}

	currentRun, ok, err := scoped.GetRun(prepared.Run.ID)
	if err != nil {
		writeSSE(w, "error", failureChatChunk(w, r, http.StatusInternalServerError, err))
		flusher.Flush()
		return
	}
	if ok && currentRun.Status == domain.RunWaitingForUser {
		writeTerminalRunDone(w, flusher, currentRun)
		h.syncMemoryTurn(userMessage, currentRun.ID)
		return
	}

	h.completeStreamingRun(w, flusher, r, runCtx, runCompletionRequest{
		WorkspaceID: workspaceID, RunID: prepared.Run.ID, ConversationID: conversationID, UserInput: req.Message,
		Assistant: assistant.String(), UserMessage: &userMessage, GenerateTitle: true,
	})
}

func (h *Handler) chatMultiAgent(w http.ResponseWriter, flusher http.Flusher, r *http.Request, req domain.ChatRequest, conversationID string, userMessage domain.Message) {
	scoped := h.scopedStoreForID(req.WorkspaceID)
	runCtx := runExecutionContext(r)
	prepared, err := h.agentRuntime.PrepareCollaborationRunWithContract(runCtx, req.AgentID, conversationID, req.CompletionContract)
	if err != nil {
		status := http.StatusInternalServerError
		if store.IsNotFound(err) {
			status = http.StatusNotFound
		}
		writeSSE(w, "error", failureChatChunk(w, r, status, err))
		flusher.Flush()
		return
	}

	writeRunStateSSE(w, flusher, conversationID, prepared.Run.ID, prepared.WorkerAgent.ID, prepared.Run.Status)

	events, errs := h.agentRuntime.RunCollaboration(runCtx, prepared, req.Message)
	var assistant strings.Builder
	for event := range events {
		writeUnifiedRunEvent(w, flusher, event, &assistant)
	}

	if err := <-errs; err != nil {
		_, _ = h.agentRuntime.FailRun(prepared.Run.ID, err)
		writeSSE(w, "error", failureChatChunk(w, r, http.StatusInternalServerError, err))
		flusher.Flush()
		return
	}

	run, ok, err := scoped.GetRun(prepared.Run.ID)
	if err != nil {
		_, _ = h.agentRuntime.FailRun(prepared.Run.ID, err)
		writeSSE(w, "error", failureChatChunk(w, r, http.StatusInternalServerError, err))
		flusher.Flush()
		return
	}
	if !ok {
		writeSSE(w, "error", failureChatChunk(w, r, http.StatusNotFound, store.ErrNotFound("run")))
		flusher.Flush()
		return
	}
	if run.Status == domain.RunWaitingForUser {
		writeTerminalRunDone(w, flusher, run)
		h.syncMemoryTurn(userMessage, run.ID)
		return
	}

	h.completeStreamingRun(w, flusher, r, runCtx, runCompletionRequest{
		WorkspaceID: req.WorkspaceID, RunID: prepared.Run.ID, ConversationID: conversationID, UserInput: req.Message,
		Assistant: assistant.String(), UserMessage: &userMessage, GenerateTitle: true,
	})
}

func (h *Handler) chatAutonomous(w http.ResponseWriter, flusher http.Flusher, r *http.Request, req domain.ChatRequest, conversationID string, userMessage domain.Message) {
	scoped := h.scopedStoreForID(req.WorkspaceID)
	runCtx := runExecutionContext(r)
	prepared, err := h.agentRuntime.PrepareAutonomousRunWithContract(runCtx, req.AgentID, conversationID, req.CompletionContract)
	if err != nil {
		status := http.StatusInternalServerError
		if store.IsNotFound(err) {
			status = http.StatusNotFound
		}
		writeSSE(w, "error", failureChatChunk(w, r, status, err))
		flusher.Flush()
		return
	}

	writeRunStateSSE(w, flusher, conversationID, prepared.Run.ID, prepared.WorkerAgent.ID, prepared.Run.Status)

	events, errs := h.agentRuntime.RunAutonomous(runCtx, prepared, req.Message)
	var assistant strings.Builder
	for event := range events {
		writeUnifiedRunEvent(w, flusher, event, &assistant)
	}

	if err := <-errs; err != nil {
		currentRun, ok, getErr := scoped.GetRun(prepared.Run.ID)
		if getErr == nil && ok && currentRun.Status == domain.RunCanceled {
			writeTerminalRunDone(w, flusher, currentRun)
			return
		}
		_, _ = h.agentRuntime.FailRun(prepared.Run.ID, err)
		writeSSE(w, "error", failureChatChunk(w, r, http.StatusInternalServerError, err))
		flusher.Flush()
		return
	}

	run, ok, err := scoped.GetRun(prepared.Run.ID)
	if err != nil {
		_, _ = h.agentRuntime.FailRun(prepared.Run.ID, err)
		writeSSE(w, "error", failureChatChunk(w, r, http.StatusInternalServerError, err))
		flusher.Flush()
		return
	}
	if !ok {
		writeSSE(w, "error", failureChatChunk(w, r, http.StatusNotFound, store.ErrNotFound("run")))
		flusher.Flush()
		return
	}
	if run.Status == domain.RunWaitingForUser || run.Status == domain.RunCanceled {
		writeTerminalRunDone(w, flusher, run)
		h.syncMemoryTurn(userMessage, run.ID)
		return
	}

	h.completeStreamingRun(w, flusher, r, runCtx, runCompletionRequest{
		WorkspaceID: req.WorkspaceID, RunID: prepared.Run.ID, ConversationID: conversationID, UserInput: req.Message,
		Assistant: assistant.String(), UserMessage: &userMessage, GenerateTitle: true,
	})
}
