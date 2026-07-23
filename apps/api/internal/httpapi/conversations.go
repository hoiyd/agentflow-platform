package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"

	"agentflow-platform/apps/api/internal/store"
)

func (h *Handler) listConversations(w http.ResponseWriter, r *http.Request) {
	conversations, err := h.store.ListConversationsByWorkspace(workspaceIDFromRequest(r))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, conversations)
}

func (h *Handler) createConversation(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title string `json:"title"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	conversation, err := h.store.CreateConversationInWorkspace(workspaceIDFromRequest(r), body.Title)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, conversation)
}

func (h *Handler) updateConversation(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/api/conversations/"))
	if id == "" || strings.Contains(id, "/") {
		writeError(w, http.StatusBadRequest, "conversation id is required")
		return
	}

	var body struct {
		Title string `json:"title"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if strings.TrimSpace(body.Title) == "" {
		writeError(w, http.StatusBadRequest, "title is required")
		return
	}

	workspaceID := workspaceIDFromRequest(r)
	if err := h.store.UpdateConversationTitleInWorkspace(workspaceID, id, body.Title); err != nil {
		if store.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "conversation not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	conversation, ok, err := h.store.GetConversationInWorkspace(workspaceID, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "conversation not found")
		return
	}

	writeJSON(w, http.StatusOK, conversation)
}

func (h *Handler) deleteConversation(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/api/conversations/"))
	if id == "" || strings.Contains(id, "/") {
		writeError(w, http.StatusBadRequest, "conversation id is required")
		return
	}

	if err := h.store.DeleteConversationInWorkspace(workspaceIDFromRequest(r), id); err != nil {
		if store.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "conversation not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}

func (h *Handler) listMessages(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/conversations/"), "/messages"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "conversation id is required")
		return
	}
	workspaceID := workspaceIDFromRequest(r)
	if _, ok, err := h.store.GetConversationInWorkspace(workspaceID, id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	} else if !ok {
		writeError(w, http.StatusNotFound, "conversation not found")
		return
	}

	messages, err := h.store.ListMessagesInWorkspace(workspaceID, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, messages)
}
