package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/failure"
	"agentflow-platform/apps/api/internal/store"
)

const maxTaskStatePatchBytes = 128 << 10

func (h *Handler) getTaskState(w http.ResponseWriter, r *http.Request) {
	conversationID := strings.TrimSpace(r.PathValue("id"))
	if conversationID == "" {
		writeError(w, http.StatusBadRequest, "conversation id is required")
		return
	}
	scoped := h.scopedStore(r)
	conversation, ok, err := scoped.GetConversation(conversationID)
	if err != nil {
		writeFailure(w, r, http.StatusInternalServerError, err)
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "conversation not found")
		return
	}
	state, ok, err := scoped.GetTaskState(conversationID)
	if err != nil {
		writeTaskStateError(w, r, err)
		return
	}
	if !ok {
		state = domain.EmptyTaskState(conversation.WorkspaceID, conversationID)
	}
	writeJSON(w, http.StatusOK, state)
}

func (h *Handler) patchTaskState(w http.ResponseWriter, r *http.Request) {
	conversationID := strings.TrimSpace(r.PathValue("id"))
	if conversationID == "" {
		writeError(w, http.StatusBadRequest, "conversation id is required")
		return
	}
	var patch domain.TaskStatePatch
	r.Body = http.MaxBytesReader(w, r.Body, maxTaskStatePatchBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&patch); err != nil {
		writeError(w, http.StatusBadRequest, "invalid task state patch")
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid task state patch")
		return
	}
	revision, err := h.scopedStore(r).ApplyTaskStatePatch(conversationID, patch, domain.TaskStateSource{ActorType: "user"})
	if err != nil {
		writeTaskStateError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, revision)
}

func (h *Handler) listTaskStateRevisions(w http.ResponseWriter, r *http.Request) {
	conversationID := strings.TrimSpace(r.PathValue("id"))
	if conversationID == "" {
		writeError(w, http.StatusBadRequest, "conversation id is required")
		return
	}
	revisions, err := h.scopedStore(r).ListTaskStateRevisions(conversationID)
	if err != nil {
		writeTaskStateError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, revisions)
}

func (h *Handler) getTaskStateRevision(w http.ResponseWriter, r *http.Request) {
	conversationID := strings.TrimSpace(r.PathValue("id"))
	version, err := strconv.ParseInt(strings.TrimSpace(r.PathValue("version")), 10, 64)
	if conversationID == "" || err != nil || version <= 0 {
		writeError(w, http.StatusBadRequest, "valid conversation id and task state version are required")
		return
	}
	revision, ok, err := h.scopedStore(r).GetTaskStateRevision(conversationID, version)
	if err != nil {
		writeTaskStateError(w, r, err)
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "task state revision not found")
		return
	}
	writeJSON(w, http.StatusOK, revision)
}

func writeTaskStateError(w http.ResponseWriter, r *http.Request, err error) {
	var conflict *store.TaskStateVersionConflict
	switch {
	case store.IsNotFound(err):
		writeFailure(w, r, http.StatusNotFound, err)
	case errors.As(err, &conflict):
		writeFailure(w, r, http.StatusConflict, err)
	case failure.Describe(err).Category == failure.CategoryValidation:
		writeFailure(w, r, http.StatusBadRequest, err)
	default:
		writeFailure(w, r, http.StatusInternalServerError, err)
	}
}
