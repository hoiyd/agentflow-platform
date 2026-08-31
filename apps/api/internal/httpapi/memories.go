package httpapi

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"

	"agentflow-platform/apps/api/internal/domain"
	memorypkg "agentflow-platform/apps/api/internal/memory"
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

func (h *Handler) syncMemoryTurn(message domain.Message, runID string) {
	if h.memories == nil {
		return
	}
	idempotencyKey := ""
	if messageID := strings.TrimSpace(message.ID); messageID != "" {
		idempotencyKey = "message:" + messageID
	}
	if err := h.memories.SyncTurn(memorypkg.TurnSyncRequest{
		RunID: strings.TrimSpace(runID), IdempotencyKey: idempotencyKey, Message: message,
	}); err != nil {
		log.Printf("memory_turn_sync_rejected run_id=%s message_id=%s error=%q", runID, message.ID, err.Error())
	}
}
