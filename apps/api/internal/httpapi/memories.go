package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"agentflow-platform/apps/api/internal/domain"
)

func (h *Handler) createMemory(w http.ResponseWriter, r *http.Request) {
	var memory domain.Memory
	if err := json.NewDecoder(r.Body).Decode(&memory); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	memory.Kind = strings.TrimSpace(memory.Kind)
	memory.Content = strings.TrimSpace(memory.Content)
	if memory.Kind == "" {
		writeError(w, http.StatusBadRequest, "memory kind is required")
		return
	}
	if memory.Content == "" {
		writeError(w, http.StatusBadRequest, "memory content is required")
		return
	}

	embedding, err := h.openAI.EmbedText(r.Context(), memory.Content)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	created, err := h.store.CreateMemory(memory, domain.MemoryEmbedding{
		Provider:   embedding.Provider,
		Model:      embedding.Model,
		Dimensions: len(embedding.Vector),
		Embedding:  embedding.Vector,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
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
	search.Query = strings.TrimSpace(search.Query)
	if search.Query == "" {
		writeError(w, http.StatusBadRequest, "query is required")
		return
	}

	embedding, err := h.openAI.EmbedText(r.Context(), search.Query)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	search.Embedding = embedding.Vector
	search.EmbeddingProvider = embedding.Provider
	search.EmbeddingModel = embedding.Model
	items, err := h.store.SearchMemories(search)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (h *Handler) rememberMessage(ctx context.Context, message domain.Message, runID string) error {
	content := strings.TrimSpace(message.Content)
	if content == "" {
		return nil
	}
	embedding, err := h.openAI.EmbedText(ctx, content)
	if err != nil {
		return err
	}
	_, err = h.store.CreateMemory(domain.Memory{
		ID:              "mem_msg_" + message.ID,
		ConversationID:  message.ConversationID,
		RunID:           strings.TrimSpace(runID),
		SourceMessageID: message.ID,
		Kind:            "message",
		Content:         content,
		Metadata: map[string]any{
			"role": message.Role,
		},
		CreatedAt: message.CreatedAt,
	}, domain.MemoryEmbedding{
		Provider:   embedding.Provider,
		Model:      embedding.Model,
		Dimensions: len(embedding.Vector),
		Embedding:  embedding.Vector,
	})
	return err
}
