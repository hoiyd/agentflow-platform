package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"agentflow-platform/apps/api/internal/domain"
	memorypkg "agentflow-platform/apps/api/internal/memory"
	"agentflow-platform/apps/api/internal/store"
)

func (h *Handler) createMemory(w http.ResponseWriter, r *http.Request) {
	var memory domain.Memory
	if err := json.NewDecoder(r.Body).Decode(&memory); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	workspaceID, matches := resolvePayloadWorkspace(r, memory.WorkspaceID)
	if !matches {
		writeError(w, http.StatusBadRequest, "workspace_id does not match request scope")
		return
	}
	memory.WorkspaceID = workspaceID
	created, err := h.memories.Commit(r.Context(), memory)
	if err != nil {
		if errors.Is(err, store.ErrMemoryConflict) {
			writeMemoryMutationFailure(w, r, err)
			return
		}
		status := http.StatusBadRequest
		if memorypkg.IsEmbeddingError(err) {
			status = http.StatusBadGateway
		}
		writeFailure(w, r, status, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (h *Handler) searchMemories(w http.ResponseWriter, r *http.Request) {
	var search domain.MemorySearch
	if err := json.NewDecoder(r.Body).Decode(&search); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	workspaceID, matches := resolvePayloadWorkspace(r, search.WorkspaceID)
	if !matches {
		writeError(w, http.StatusBadRequest, "workspace_id does not match request scope")
		return
	}
	search.WorkspaceID = workspaceID
	items, err := h.memories.Recall(r.Context(), search)
	if err != nil {
		status := http.StatusBadRequest
		if memorypkg.IsEmbeddingError(err) {
			status = http.StatusBadGateway
		}
		writeFailure(w, r, status, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}
