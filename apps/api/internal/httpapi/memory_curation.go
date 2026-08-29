package httpapi

import (
	"log"
	"strings"

	"agentflow-platform/apps/api/internal/domain"
	memorypkg "agentflow-platform/apps/api/internal/memory"
)

func (h *Handler) enqueueMemoryCuration(message domain.Message, runID string) {
	if h.memoryCuration == nil {
		return
	}
	if err := h.memoryCuration.Enqueue(memorypkg.CurationJob{RunID: strings.TrimSpace(runID), Message: message}); err != nil {
		log.Printf("memory_curation_enqueue_failed run_id=%s message_id=%s error=%q", runID, message.ID, err.Error())
	}
}
