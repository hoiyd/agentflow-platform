package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	agentpkg "agentflow-platform/apps/api/internal/agent"
	"agentflow-platform/apps/api/internal/domain"
)

func (h *Handler) chat(w http.ResponseWriter, r *http.Request) {
	var req domain.ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	req.Message = strings.TrimSpace(req.Message)
	if req.Message == "" {
		writeError(w, http.StatusBadRequest, "message is required")
		return
	}

	conversationID := strings.TrimSpace(req.ConversationID)
	if conversationID == "" {
		conversation, err := h.store.CreateConversation(req.Message)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		conversationID = conversation.ID
	} else if _, ok, err := h.store.GetConversation(conversationID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	} else if !ok {
		writeError(w, http.StatusNotFound, "conversation not found")
		return
	}

	if _, err := h.store.AddMessage(conversationID, "user", req.Message); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	history, err := h.store.ListMessages(conversationID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
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
		h.chatAutonomous(w, flusher, r, req, conversationID)
		return
	}
	if mode == agentpkg.ChatModeMultiAgent {
		h.chatMultiAgent(w, flusher, r, req, conversationID)
		return
	}

	prepared, err := h.agentRuntime.PrepareChatRun(r.Context(), req.AgentID, conversationID)
	if err != nil {
		status := http.StatusInternalServerError
		if strings.Contains(err.Error(), "not found") {
			status = http.StatusNotFound
		}
		writeSSE(w, "error", domain.ChatChunk{Type: "error", Error: http.StatusText(status) + ": " + err.Error()})
		flusher.Flush()
		return
	}
	writeSSE(w, "run", domain.ChatChunk{
		Type:           "run",
		ConversationID: conversationID,
		RunID:          prepared.Run.ID,
		AgentID:        prepared.Agent.ID,
		Status:         string(prepared.Run.Status),
	})
	flusher.Flush()

	events, errs := h.agentRuntime.StreamChat(r.Context(), prepared, history, req.Message)
	var assistant strings.Builder
	for event := range events {
		switch event.Type {
		case "delta":
			assistant.WriteString(event.Delta)
			writeSSE(w, "delta", domain.ChatChunk{Type: "delta", Delta: event.Delta})
		}
		flusher.Flush()
	}

	if err := <-errs; err != nil {
		_, _ = h.agentRuntime.FailRun(prepared.Run.ID, err)
		writeSSE(w, "error", domain.ChatChunk{Type: "error", Error: err.Error()})
		flusher.Flush()
		return
	}

	currentRun, ok, err := h.store.GetRun(prepared.Run.ID)
	if err != nil {
		writeSSE(w, "error", domain.ChatChunk{Type: "error", Error: err.Error()})
		flusher.Flush()
		return
	}
	if ok && currentRun.Status == domain.RunWaitingForUser {
		writeSSE(w, "done", domain.ChatChunk{
			Type:           "done",
			ConversationID: conversationID,
			RunID:          currentRun.ID,
			AgentID:        currentRun.AgentID,
			Status:         string(currentRun.Status),
		})
		flusher.Flush()
		return
	}

	message, err := h.store.AddMessage(conversationID, "assistant", assistant.String())
	if err != nil {
		_, _ = h.agentRuntime.FailRun(prepared.Run.ID, err)
		writeSSE(w, "error", domain.ChatChunk{Type: "error", Error: err.Error()})
		flusher.Flush()
		return
	}

	completed, err := h.agentRuntime.CompleteRun(prepared.Run.ID)
	if err != nil {
		writeSSE(w, "error", domain.ChatChunk{Type: "error", Error: err.Error()})
		flusher.Flush()
		return
	}

	writeSSE(w, "done", domain.ChatChunk{
		Type:           "done",
		ConversationID: conversationID,
		RunID:          completed.ID,
		AgentID:        completed.AgentID,
		Status:         string(completed.Status),
		MessageID:      message.ID,
	})
	flusher.Flush()
}

func (h *Handler) chatMultiAgent(w http.ResponseWriter, flusher http.Flusher, r *http.Request, req domain.ChatRequest, conversationID string) {
	prepared, err := h.agentRuntime.PrepareCollaborationRun(r.Context(), req.AgentID, conversationID)
	if err != nil {
		status := http.StatusInternalServerError
		if strings.Contains(err.Error(), "not found") {
			status = http.StatusNotFound
		}
		writeSSE(w, "error", domain.ChatChunk{Type: "error", Error: http.StatusText(status) + ": " + err.Error()})
		flusher.Flush()
		return
	}

	writeSSE(w, "run", domain.ChatChunk{
		Type:           "run",
		ConversationID: conversationID,
		RunID:          prepared.Run.ID,
		AgentID:        prepared.WorkerAgent.ID,
		Status:         string(prepared.Run.Status),
	})
	flusher.Flush()

	events, errs := h.agentRuntime.RunCollaboration(r.Context(), prepared, req.Message)
	var assistant strings.Builder
	for event := range events {
		switch event.Type {
		case "run":
			writeSSE(w, "run", domain.ChatChunk{
				Type:           "run",
				ConversationID: event.Run.ConversationID,
				RunID:          event.Run.ID,
				AgentID:        event.Run.AgentID,
				Status:         string(event.Run.Status),
			})
		case "collaboration_step":
			writeSSE(w, "collaboration_step", domain.ChatChunk{
				Type:           "collaboration_step",
				ConversationID: event.Step.ConversationID,
				RunID:          event.Step.RunID,
				AgentID:        event.Step.AgentID,
				Status:         string(event.Step.Status),
				Role:           event.Step.Role,
				Iteration:      event.Step.Iteration,
				Input:          event.Step.Input,
				Output:         event.Step.Output,
				Error:          event.Step.Error,
			})
		case "delta":
			assistant.WriteString(event.Delta)
			writeSSE(w, "delta", domain.ChatChunk{Type: "delta", Delta: event.Delta})
		}
		flusher.Flush()
	}

	if err := <-errs; err != nil {
		_, _ = h.agentRuntime.FailRun(prepared.Run.ID, err)
		writeSSE(w, "error", domain.ChatChunk{Type: "error", Error: err.Error()})
		flusher.Flush()
		return
	}

	run, ok, err := h.store.GetRun(prepared.Run.ID)
	if err != nil {
		_, _ = h.agentRuntime.FailRun(prepared.Run.ID, err)
		writeSSE(w, "error", domain.ChatChunk{Type: "error", Error: err.Error()})
		flusher.Flush()
		return
	}
	if !ok {
		writeSSE(w, "error", domain.ChatChunk{Type: "error", Error: "run not found"})
		flusher.Flush()
		return
	}
	if run.Status == domain.RunWaitingForUser {
		writeSSE(w, "done", domain.ChatChunk{
			Type:           "done",
			ConversationID: conversationID,
			RunID:          run.ID,
			AgentID:        run.AgentID,
			Status:         string(run.Status),
		})
		flusher.Flush()
		return
	}

	message, err := h.store.AddMessage(conversationID, "assistant", assistant.String())
	if err != nil {
		_, _ = h.agentRuntime.FailRun(prepared.Run.ID, err)
		writeSSE(w, "error", domain.ChatChunk{Type: "error", Error: err.Error()})
		flusher.Flush()
		return
	}

	completed, err := h.agentRuntime.CompleteRun(prepared.Run.ID)
	if err != nil {
		writeSSE(w, "error", domain.ChatChunk{Type: "error", Error: err.Error()})
		flusher.Flush()
		return
	}

	writeSSE(w, "done", domain.ChatChunk{
		Type:           "done",
		ConversationID: conversationID,
		RunID:          completed.ID,
		AgentID:        completed.AgentID,
		Status:         string(completed.Status),
		MessageID:      message.ID,
	})
	flusher.Flush()
}

func (h *Handler) chatAutonomous(w http.ResponseWriter, flusher http.Flusher, r *http.Request, req domain.ChatRequest, conversationID string) {
	prepared, err := h.agentRuntime.PrepareAutonomousRun(r.Context(), conversationID)
	if err != nil {
		status := http.StatusInternalServerError
		if strings.Contains(err.Error(), "not found") {
			status = http.StatusNotFound
		}
		writeSSE(w, "error", domain.ChatChunk{Type: "error", Error: http.StatusText(status) + ": " + err.Error()})
		flusher.Flush()
		return
	}

	writeSSE(w, "run", domain.ChatChunk{
		Type:           "run",
		ConversationID: conversationID,
		RunID:          prepared.Run.ID,
		AgentID:        prepared.WorkerAgent.ID,
		Status:         string(prepared.Run.Status),
	})
	flusher.Flush()

	events, errs := h.agentRuntime.RunAutonomous(r.Context(), prepared, req.Message)
	var assistant strings.Builder
	for event := range events {
		switch event.Type {
		case "run":
			writeSSE(w, "run", domain.ChatChunk{
				Type:           "run",
				ConversationID: event.Run.ConversationID,
				RunID:          event.Run.ID,
				AgentID:        event.Run.AgentID,
				Status:         string(event.Run.Status),
			})
		case "collaboration_step":
			writeSSE(w, "collaboration_step", domain.ChatChunk{
				Type:           "collaboration_step",
				ConversationID: event.Step.ConversationID,
				RunID:          event.Step.RunID,
				AgentID:        event.Step.AgentID,
				Status:         string(event.Step.Status),
				Role:           event.Step.Role,
				Iteration:      event.Step.Iteration,
				Input:          event.Step.Input,
				Output:         event.Step.Output,
				Error:          event.Step.Error,
			})
		case "delta":
			assistant.WriteString(event.Delta)
			writeSSE(w, "delta", domain.ChatChunk{Type: "delta", Delta: event.Delta})
		}
		flusher.Flush()
	}

	if err := <-errs; err != nil {
		currentRun, ok, getErr := h.store.GetRun(prepared.Run.ID)
		if getErr == nil && ok && currentRun.Status == domain.RunCanceled {
			writeSSE(w, "done", domain.ChatChunk{
				Type:           "done",
				ConversationID: conversationID,
				RunID:          currentRun.ID,
				AgentID:        currentRun.AgentID,
				Status:         string(currentRun.Status),
			})
			flusher.Flush()
			return
		}
		_, _ = h.agentRuntime.FailRun(prepared.Run.ID, err)
		writeSSE(w, "error", domain.ChatChunk{Type: "error", Error: err.Error()})
		flusher.Flush()
		return
	}

	run, ok, err := h.store.GetRun(prepared.Run.ID)
	if err != nil {
		_, _ = h.agentRuntime.FailRun(prepared.Run.ID, err)
		writeSSE(w, "error", domain.ChatChunk{Type: "error", Error: err.Error()})
		flusher.Flush()
		return
	}
	if !ok {
		writeSSE(w, "error", domain.ChatChunk{Type: "error", Error: "run not found"})
		flusher.Flush()
		return
	}
	if run.Status == domain.RunCanceled {
		writeSSE(w, "done", domain.ChatChunk{
			Type:           "done",
			ConversationID: conversationID,
			RunID:          run.ID,
			AgentID:        run.AgentID,
			Status:         string(run.Status),
		})
		flusher.Flush()
		return
	}

	message, err := h.store.AddMessage(conversationID, "assistant", assistant.String())
	if err != nil {
		_, _ = h.agentRuntime.FailRun(prepared.Run.ID, err)
		writeSSE(w, "error", domain.ChatChunk{Type: "error", Error: err.Error()})
		flusher.Flush()
		return
	}

	completed, err := h.agentRuntime.CompleteRun(prepared.Run.ID)
	if err != nil {
		writeSSE(w, "error", domain.ChatChunk{Type: "error", Error: err.Error()})
		flusher.Flush()
		return
	}

	writeSSE(w, "done", domain.ChatChunk{
		Type:           "done",
		ConversationID: conversationID,
		RunID:          completed.ID,
		AgentID:        completed.AgentID,
		Status:         string(completed.Status),
		MessageID:      message.ID,
	})
	flusher.Flush()
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

	run, ok, err := h.store.GetRun(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "run not found")
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

	writeSSE(w, "run", domain.ChatChunk{
		Type:           "run",
		ConversationID: run.ConversationID,
		RunID:          run.ID,
		AgentID:        run.AgentID,
		Status:         string(run.Status),
	})
	flusher.Flush()

	events, errs := h.agentRuntime.ContinueCollaboration(r.Context(), id, req.Plan)
	var assistant strings.Builder
	for event := range events {
		switch event.Type {
		case "run":
			writeSSE(w, "run", domain.ChatChunk{
				Type:           "run",
				ConversationID: event.Run.ConversationID,
				RunID:          event.Run.ID,
				AgentID:        event.Run.AgentID,
				Status:         string(event.Run.Status),
			})
		case "collaboration_step":
			writeSSE(w, "collaboration_step", domain.ChatChunk{
				Type:           "collaboration_step",
				ConversationID: event.Step.ConversationID,
				RunID:          event.Step.RunID,
				AgentID:        event.Step.AgentID,
				Status:         string(event.Step.Status),
				Role:           event.Step.Role,
				Iteration:      event.Step.Iteration,
				Input:          event.Step.Input,
				Output:         event.Step.Output,
				Error:          event.Step.Error,
			})
		case "delta":
			assistant.WriteString(event.Delta)
			writeSSE(w, "delta", domain.ChatChunk{Type: "delta", Delta: event.Delta})
		}
		flusher.Flush()
	}

	if err := <-errs; err != nil {
		_, _ = h.agentRuntime.FailRun(id, err)
		writeSSE(w, "error", domain.ChatChunk{Type: "error", Error: err.Error()})
		flusher.Flush()
		return
	}

	message, err := h.store.AddMessage(run.ConversationID, "assistant", assistant.String())
	if err != nil {
		_, _ = h.agentRuntime.FailRun(id, err)
		writeSSE(w, "error", domain.ChatChunk{Type: "error", Error: err.Error()})
		flusher.Flush()
		return
	}

	completed, err := h.agentRuntime.CompleteRun(id)
	if err != nil {
		writeSSE(w, "error", domain.ChatChunk{Type: "error", Error: err.Error()})
		flusher.Flush()
		return
	}

	writeSSE(w, "done", domain.ChatChunk{
		Type:           "done",
		ConversationID: completed.ConversationID,
		RunID:          completed.ID,
		AgentID:        completed.AgentID,
		Status:         string(completed.Status),
		MessageID:      message.ID,
	})
	flusher.Flush()
}

func writeSSE(w http.ResponseWriter, event string, value any) {
	bytes, _ := json.Marshal(value)
	_, _ = fmt.Fprintf(w, "event: %s\n", event)
	_, _ = fmt.Fprintf(w, "data: %s\n\n", string(bytes))
}
