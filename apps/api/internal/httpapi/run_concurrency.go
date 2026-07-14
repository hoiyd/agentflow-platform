package httpapi

import (
	"errors"
	"net/http"

	"agentflow-platform/apps/api/internal/concurrency"
)

const overloadRetryAfterSeconds = "1"

func (h *Handler) reserveRunCapacity(w http.ResponseWriter) (*concurrency.Reservation, bool) {
	reservation, err := h.runController.Reserve()
	if err == nil {
		return reservation, true
	}
	w.Header().Set("Retry-After", overloadRetryAfterSeconds)
	writeError(w, http.StatusTooManyRequests, err.Error())
	return nil, false
}

func (h *Handler) acquireRunSlot(w http.ResponseWriter, r *http.Request, reservation *concurrency.Reservation, conversationID string) (func(), bool) {
	release, err := reservation.Start(r.Context(), conversationID)
	if err == nil {
		return release, true
	}
	if errors.Is(err, r.Context().Err()) {
		return nil, false
	}
	w.Header().Set("Retry-After", overloadRetryAfterSeconds)
	status := http.StatusServiceUnavailable
	if errors.Is(err, concurrency.ErrQueueFull) {
		status = http.StatusTooManyRequests
	}
	writeError(w, status, err.Error())
	return nil, false
}
