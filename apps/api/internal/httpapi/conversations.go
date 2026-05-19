package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
)

func (h *Handler) listConversations(w http.ResponseWriter, r *http.Request) {
	conversations, err := h.store.ListConversations()
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

	conversation, err := h.store.CreateConversation(body.Title)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, conversation)
}

func (h *Handler) listMessages(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/conversations/"), "/messages"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "conversation id is required")
		return
	}
	if _, ok, err := h.store.GetConversation(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	} else if !ok {
		writeError(w, http.StatusNotFound, "conversation not found")
		return
	}

	messages, err := h.store.ListMessages(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, messages)
}
