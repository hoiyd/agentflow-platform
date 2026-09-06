package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"agentflow-platform/apps/api/internal/domain"
	memorypkg "agentflow-platform/apps/api/internal/memory"
	"agentflow-platform/apps/api/internal/store"
)

func (h *Handler) getMemory(w http.ResponseWriter, r *http.Request) {
	provider, ok := h.memories.(memorypkg.Administration)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "memory administration is unavailable")
		return
	}
	detail, err := provider.GetMemory(r.Context(), workspaceIDFromRequest(r), r.PathValue("id"))
	if err != nil {
		writeMemoryMutationFailure(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

func (h *Handler) mutateMemory(w http.ResponseWriter, r *http.Request) {
	provider, ok := h.memories.(memorypkg.Administration)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "memory administration is unavailable")
		return
	}
	var command domain.MemoryMutation
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&command); err != nil {
		writeError(w, http.StatusBadRequest, "invalid memory mutation body")
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeError(w, http.StatusBadRequest, "expected one memory mutation")
		return
	}
	result, err := provider.MutateMemory(r.Context(), workspaceIDFromRequest(r), r.PathValue("id"), command)
	if err != nil {
		writeMemoryMutationFailure(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func writeMemoryMutationFailure(w http.ResponseWriter, r *http.Request, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, store.ErrMemoryMissing):
		status = http.StatusNotFound
	case errors.Is(err, store.ErrMemoryConflict):
		status = http.StatusConflict
	case errors.Is(err, store.ErrMemoryMutationInvalid):
		status = http.StatusBadRequest
	case memorypkg.IsEmbeddingError(err):
		status = http.StatusBadGateway
	}
	writeFailure(w, r, status, err)
}
