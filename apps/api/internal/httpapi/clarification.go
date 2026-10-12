package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"agentflow-platform/apps/api/app/runcommands"
	"agentflow-platform/apps/api/internal/clarification"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/store"
)

func (h *Handler) requireClarificationRun(w http.ResponseWriter, r *http.Request) bool {
	_, ok, err := h.store.ForWorkspace(domain.NewWorkspaceScope(workspaceIDFromRequest(r))).GetRun(r.PathValue("id"))
	if err != nil {
		writeFailure(w, r, 500, err)
		return false
	}
	if !ok {
		writeError(w, 404, "run not found")
		return false
	}
	return true
}

func clarificationStatus(err error) int {
	if errors.Is(err, clarification.ErrInvalid) {
		return http.StatusBadRequest
	}
	if errors.Is(err, clarification.ErrConflict) {
		return http.StatusConflict
	}
	if store.IsNotFound(err) {
		return http.StatusNotFound
	}
	return http.StatusInternalServerError
}

func (h *Handler) listClarifications(w http.ResponseWriter, r *http.Request) {
	if !h.requireClarificationRun(w, r) {
		return
	}
	items, err := h.clarifications.ListClarifications(r.Context(), workspaceOwner(r), workspaceIDFromRequest(r), r.PathValue("id"))
	if err != nil {
		writeFailure(w, r, clarificationStatus(err), err)
		return
	}
	writeJSON(w, 200, items)
}

func (h *Handler) answerClarification(w http.ResponseWriter, r *http.Request) {
	if !h.requireClarificationRun(w, r) {
		return
	}
	var input domain.ClarificationAnswer
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeError(w, 400, "invalid clarification answer")
		return
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		writeError(w, 400, "invalid clarification answer")
		return
	}
	item, err := h.clarifications.AnswerClarification(r.Context(), workspaceOwner(r), workspaceIDFromRequest(r), r.PathValue("id"), r.PathValue("question_id"), input)
	if err != nil {
		writeFailure(w, r, clarificationStatus(err), err)
		return
	}
	writeJSON(w, 200, item)
}

func (h *Handler) resumeClarification(w http.ResponseWriter, r *http.Request) {
	if !h.requireClarificationRun(w, r) {
		return
	}
	h.executeRunCommand(w, r, func(observer runcommands.Observer) error {
		return h.runCommands().ResumeClarification(r.Context(), commandScope(r, workspaceIDFromRequest(r)), r.PathValue("id"), r.PathValue("question_id"), observer)
	})
}

func (h *Handler) withdrawClarification(w http.ResponseWriter, r *http.Request) {
	if !h.requireClarificationRun(w, r) {
		return
	}
	var input struct {
		Version int64 `json:"version"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		writeError(w, 400, "invalid question version")
		return
	}
	item, err := h.clarifications.WithdrawClarification(r.Context(), workspaceOwner(r), workspaceIDFromRequest(r), r.PathValue("id"), r.PathValue("question_id"), input.Version)
	if err == nil {
		_, err = h.agentRuntime.CancelRun(r.PathValue("id"))
	}
	if err != nil {
		writeFailure(w, r, clarificationStatus(err), err)
		return
	}
	writeJSON(w, 200, item)
}

func (h *Handler) hasBlockingClarification(w http.ResponseWriter, r *http.Request, runID string) bool {
	if h.clarifications == nil {
		return false
	}
	items, err := h.clarifications.ListClarifications(r.Context(), workspaceOwner(r), workspaceIDFromRequest(r), runID)
	if err != nil {
		writeFailure(w, r, 500, err)
		return true
	}
	for _, q := range items {
		if q.Status == "pending" || q.Status == "answered" || q.Status == "expired" {
			writeFailure(w, r, 409, clarification.ErrConflict)
			return true
		}
	}
	return false
}
