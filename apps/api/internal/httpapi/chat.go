package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	agentpkg "agentflow-platform/apps/api/internal/agent"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/store"
)

func (h *Handler) chat(w http.ResponseWriter, r *http.Request) {
	var req domain.ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	workspaceID, matches := resolvePayloadWorkspace(r, req.WorkspaceID)
	if !matches {
		writeError(w, http.StatusBadRequest, "workspace_id does not match request scope")
		return
	}
	req.WorkspaceID = workspaceID
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

	prepared, err := h.agentRuntime.PrepareChatRunWithContract(r.Context(), req.AgentID, conversationID, req.Executor, req.CompletionContract)
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

	events, errs := h.agentRuntime.StreamChat(r.Context(), prepared, history, req.Message, req.Executor)
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
		h.enqueueMemoryCuration(userMessage, currentRun.ID)
		return
	}

	h.completeStreamingRun(w, flusher, r, r.Context(), runCompletionRequest{
		WorkspaceID: workspaceID, RunID: prepared.Run.ID, ConversationID: conversationID, UserInput: req.Message,
		Assistant: assistant.String(), UserMessage: &userMessage, GenerateTitle: true,
	})
}

func (h *Handler) chatMultiAgent(w http.ResponseWriter, flusher http.Flusher, r *http.Request, req domain.ChatRequest, conversationID string, userMessage domain.Message) {
	scoped := h.scopedStoreForID(req.WorkspaceID)
	prepared, err := h.agentRuntime.PrepareCollaborationRunWithContract(r.Context(), req.AgentID, conversationID, req.CompletionContract)
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

	events, errs := h.agentRuntime.RunCollaboration(r.Context(), prepared, req.Message)
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
		h.enqueueMemoryCuration(userMessage, run.ID)
		return
	}

	h.completeStreamingRun(w, flusher, r, r.Context(), runCompletionRequest{
		WorkspaceID: req.WorkspaceID, RunID: prepared.Run.ID, ConversationID: conversationID, UserInput: req.Message,
		Assistant: assistant.String(), UserMessage: &userMessage, GenerateTitle: true,
	})
}

func (h *Handler) chatAutonomous(w http.ResponseWriter, flusher http.Flusher, r *http.Request, req domain.ChatRequest, conversationID string, userMessage domain.Message) {
	scoped := h.scopedStoreForID(req.WorkspaceID)
	prepared, err := h.agentRuntime.PrepareAutonomousRunWithContract(r.Context(), req.AgentID, conversationID, req.CompletionContract)
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

	events, errs := h.agentRuntime.RunAutonomous(r.Context(), prepared, req.Message)
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
		h.enqueueMemoryCuration(userMessage, run.ID)
		return
	}

	h.completeStreamingRun(w, flusher, r, r.Context(), runCompletionRequest{
		WorkspaceID: req.WorkspaceID, RunID: prepared.Run.ID, ConversationID: conversationID, UserInput: req.Message,
		Assistant: assistant.String(), UserMessage: &userMessage, GenerateTitle: true,
	})
}

func (h *Handler) continueRun(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/api/runs/"))
	id = strings.TrimSpace(strings.TrimSuffix(id, "/continue"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "run id is required")
		return
	}

	var req domain.ContinueRunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}

	scoped := h.scopedStore(r)
	run, ok, err := scoped.GetRun(id)
	if err != nil {
		writeFailure(w, r, http.StatusInternalServerError, err)
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "run not found")
		return
	}
	reservation, admitted := h.reserveRunCapacity(w, r)
	if !admitted {
		return
	}
	defer reservation.Cancel()
	releaseRun, admitted := h.acquireRunSlot(w, r, reservation, run.ConversationID)
	if !admitted {
		return
	}
	defer releaseRun()

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming is not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	writeRunStateSSE(w, flusher, run.ConversationID, run.ID, run.AgentID, run.Status)

	events, errs := h.agentRuntime.ContinueCollaboration(r.Context(), id, req.Plan)
	var assistant strings.Builder
	for event := range events {
		writeUnifiedRunEvent(w, flusher, event, &assistant)
	}

	if err := <-errs; err != nil {
		if !strings.Contains(err.Error(), "not waiting for user input") {
			_, _ = h.agentRuntime.FailRun(id, err)
		}
		writeSSE(w, "error", failureChatChunk(w, r, http.StatusInternalServerError, err))
		flusher.Flush()
		return
	}

	h.completeStreamingRun(w, flusher, r, r.Context(), runCompletionRequest{
		WorkspaceID: run.WorkspaceID, RunID: id, ConversationID: run.ConversationID, Assistant: assistant.String(),
	})
}

func (h *Handler) resumeRun(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/api/runs/"))
	id = strings.TrimSpace(strings.TrimSuffix(id, "/resume"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "run id is required")
		return
	}

	var req domain.ResumeRunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	req.UserInput = strings.TrimSpace(req.UserInput)

	scoped := h.scopedStore(r)
	run, ok, err := scoped.GetRun(id)
	if err != nil {
		writeFailure(w, r, http.StatusInternalServerError, err)
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "run not found")
		return
	}
	if run.Status == domain.RunWaitingForUser && req.UserInput == "" {
		writeError(w, http.StatusBadRequest, "user input is required")
		return
	}
	if run.Status != domain.RunWaitingForUser && run.Status != domain.RunFailedRecoverable {
		writeError(w, http.StatusBadRequest, "run is not resumable")
		return
	}
	reservation, admitted := h.reserveRunCapacity(w, r)
	if !admitted {
		return
	}
	defer reservation.Cancel()
	releaseRun, admitted := h.acquireRunSlot(w, r, reservation, run.ConversationID)
	if !admitted {
		return
	}
	defer releaseRun()

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming is not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	writeRunStateSSE(w, flusher, run.ConversationID, run.ID, run.AgentID, run.Status)

	resumeCtx := detachedRequestContext(r)
	events, errs := h.agentRuntime.ResumeAutonomous(resumeCtx, id, req.UserInput)
	if run.Status == domain.RunFailedRecoverable {
		events, errs = h.agentRuntime.ResumeRecoverableAutonomous(resumeCtx, id, req.UserInput)
	}
	var assistant strings.Builder
	for event := range events {
		writeUnifiedRunEvent(w, flusher, event, &assistant)
	}

	if err := <-errs; err != nil {
		if !strings.Contains(err.Error(), "not waiting for user input") && !strings.Contains(err.Error(), "not recoverable") {
			_, _ = h.agentRuntime.FailRun(id, err)
		}
		writeSSE(w, "error", failureChatChunk(w, r, http.StatusInternalServerError, err))
		flusher.Flush()
		return
	}

	current, ok, err := scoped.GetRun(id)
	if err != nil {
		_, _ = h.agentRuntime.FailRun(id, err)
		writeSSE(w, "error", failureChatChunk(w, r, http.StatusInternalServerError, err))
		flusher.Flush()
		return
	}
	if !ok {
		writeSSE(w, "error", failureChatChunk(w, r, http.StatusNotFound, store.ErrNotFound("run")))
		flusher.Flush()
		return
	}
	if current.Status == domain.RunWaitingForUser || current.Status == domain.RunCanceled {
		writeTerminalRunDone(w, flusher, current)
		return
	}

	h.completeStreamingRun(w, flusher, r, r.Context(), runCompletionRequest{
		WorkspaceID: current.WorkspaceID, RunID: id, ConversationID: current.ConversationID, Assistant: assistant.String(),
	})
}

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

func detachedRequestContext(r *http.Request) context.Context {
	return context.WithoutCancel(r.Context())
}

func writeSSE(w http.ResponseWriter, event string, value any) {
	bytes, _ := json.Marshal(value)
	_, _ = fmt.Fprintf(w, "event: %s\n", event)
	_, _ = fmt.Fprintf(w, "data: %s\n\n", string(bytes))
}
