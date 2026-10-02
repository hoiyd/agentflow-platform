package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

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
	if !h.authorizeMemorySource(w, r, &memory) {
		return
	}
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

// Explicit Memory may reference existing evidence, but cannot manufacture a
// provenance link across Workspace or Conversation boundaries.
func (h *Handler) authorizeMemorySource(w http.ResponseWriter, r *http.Request, memory *domain.Memory) bool {
	memory.ConversationID = strings.TrimSpace(memory.ConversationID)
	memory.RunID = strings.TrimSpace(memory.RunID)
	memory.SourceMessageID = strings.TrimSpace(memory.SourceMessageID)
	if memory.ConversationID == "" && memory.RunID == "" && memory.SourceMessageID == "" {
		return true
	}
	scoped := h.scopedStoreForID(memory.WorkspaceID)
	if memory.RunID != "" {
		run, ok, err := scoped.GetRun(memory.RunID)
		if err != nil {
			writeFailure(w, r, http.StatusInternalServerError, err)
			return false
		}
		if !ok {
			writeError(w, http.StatusNotFound, "memory source not found")
			return false
		}
		if memory.ConversationID != "" && memory.ConversationID != run.ConversationID {
			writeError(w, http.StatusBadRequest, "memory source run does not belong to conversation")
			return false
		}
		memory.ConversationID = run.ConversationID
	}
	if memory.ConversationID == "" {
		writeError(w, http.StatusBadRequest, "source_message_id requires conversation_id or run_id")
		return false
	}
	if _, ok, err := scoped.GetConversation(memory.ConversationID); err != nil {
		writeFailure(w, r, http.StatusInternalServerError, err)
		return false
	} else if !ok {
		writeError(w, http.StatusNotFound, "memory source not found")
		return false
	}
	if memory.SourceMessageID == "" {
		return true
	}
	messages, err := scoped.ListMessages(memory.ConversationID)
	if err != nil {
		writeFailure(w, r, http.StatusInternalServerError, err)
		return false
	}
	for _, message := range messages {
		if message.ID == memory.SourceMessageID {
			return true
		}
	}
	writeError(w, http.StatusNotFound, "memory source not found")
	return false
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
