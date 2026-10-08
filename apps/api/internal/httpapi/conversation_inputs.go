package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"agentflow-platform/apps/api/internal/apicontract"
	"agentflow-platform/apps/api/internal/concurrency"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/store"
)

func inputFailure(w http.ResponseWriter, r *http.Request, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, store.ErrInputInvalid):
		status = http.StatusBadRequest
	case errors.Is(err, store.ErrInputConflict):
		status = http.StatusConflict
	case errors.Is(err, store.ErrInputCapacity):
		status = http.StatusTooManyRequests
		w.Header().Set("Retry-After", "60")
	case errors.Is(err, concurrency.ErrQueueFull):
		status = http.StatusTooManyRequests
		w.Header().Set("Retry-After", overloadRetryAfterSeconds)
	case errors.Is(err, concurrency.ErrShuttingDown):
		status = http.StatusServiceUnavailable
		w.Header().Set("Retry-After", overloadRetryAfterSeconds)
	case store.IsNotFound(err):
		status = http.StatusNotFound
	}
	writeFailure(w, r, status, err)
}

func (h *Handler) listInputs(w http.ResponseWriter, r *http.Request) {
	items, err := h.inbox.ListRunInputs(r.Context(), workspaceOwner(r), workspaceIDFromRequest(r), r.PathValue("id"))
	if err != nil {
		inputFailure(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (h *Handler) enqueueInput(w http.ResponseWriter, r *http.Request) {
	var wire apicontract.RunInputRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		writeError(w, http.StatusBadRequest, "invalid input body")
		return
	}
	input := domain.RunInputRequest{Kind: string(wire.Kind), RunID: wire.RunId, Content: wire.Content, IdempotencyKey: wire.IdempotencyKey}
	if wire.Mode != nil {
		input.Mode = string(*wire.Mode)
	}
	if wire.AgentId != nil {
		input.AgentID = *wire.AgentId
	}
	if rejectCredentialContent(w, r, input) {
		return
	}
	item, err := h.inbox.EnqueueRunInput(r.Context(), workspaceOwner(r), workspaceIDFromRequest(r), r.PathValue("id"), input)
	if err != nil {
		inputFailure(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, item)
	if item.Kind == "follow_up" && item.Status == "queued" {
		h.scheduleFollowup(r, item.ConversationID, false)
	}
}

func (h *Handler) withdrawInput(w http.ResponseWriter, r *http.Request) {
	item, err := h.inbox.WithdrawRunInput(r.Context(), workspaceOwner(r), workspaceIDFromRequest(r), r.PathValue("id"), r.PathValue("inputID"))
	if err != nil {
		inputFailure(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (h *Handler) startFollowup(w http.ResponseWriter, r *http.Request) {
	if err := h.dispatchFollowup(r, r.PathValue("id"), true); err != nil {
		inputFailure(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "accepted"})
}
