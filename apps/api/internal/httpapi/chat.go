package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

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

func writeSSE(w http.ResponseWriter, event string, value any) {
	bytes, _ := json.Marshal(value)
	_, _ = fmt.Fprintf(w, "event: %s\n", event)
	_, _ = fmt.Fprintf(w, "data: %s\n\n", string(bytes))
}
