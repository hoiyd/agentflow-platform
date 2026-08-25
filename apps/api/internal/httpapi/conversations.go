package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"

	"agentflow-platform/apps/api/internal/store"
)

func (h *Handler) listConversations(w http.ResponseWriter, r *http.Request) {
	conversations, err := h.scopedStore(r).ListConversations()
	if err != nil {
		writeFailure(w, r, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, conversations)
}

func (h *Handler) createConversation(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title string `json:"title"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	conversation, err := h.scopedStore(r).CreateConversation(body.Title)
	if err != nil {
		writeFailure(w, r, http.StatusInternalServerError, err)
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

	scoped := h.scopedStore(r)
	if err := scoped.UpdateConversationTitle(id, body.Title); err != nil {
		if store.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "conversation not found")
			return
		}
		writeFailure(w, r, http.StatusInternalServerError, err)
		return
	}
	conversation, ok, err := scoped.GetConversation(id)
	if err != nil {
		writeFailure(w, r, http.StatusInternalServerError, err)
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

	if err := h.scopedStore(r).DeleteConversation(id); err != nil {
		if store.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "conversation not found")
			return
		}
		writeFailure(w, r, http.StatusInternalServerError, err)
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
	scoped := h.scopedStore(r)
	if _, ok, err := scoped.GetConversation(id); err != nil {
		writeFailure(w, r, http.StatusInternalServerError, err)
		return
	} else if !ok {
		writeError(w, http.StatusNotFound, "conversation not found")
		return
	}

	messages, err := scoped.ListMessages(id)
	if err != nil {
		writeFailure(w, r, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, messages)
}
