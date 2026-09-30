package runcompletion

import (
	"log"
	"strings"

	"agentflow-platform/apps/api/internal/domain"
	memorypkg "agentflow-platform/apps/api/internal/memory"
)

// SyncMemoryTurn only enqueues auxiliary work; rejection cannot change the Run.
func SyncMemoryTurn(memories memorypkg.TurnSyncer, message domain.Message, runID string) {
	if memories == nil {
		return
	}
	idempotencyKey := ""
	if messageID := strings.TrimSpace(message.ID); messageID != "" {
		idempotencyKey = "message:" + messageID
	}
	if err := memories.SyncTurn(memorypkg.TurnSyncRequest{
		RunID: strings.TrimSpace(runID), IdempotencyKey: idempotencyKey, Message: message,
	}); err != nil {
		log.Printf("memory_turn_sync_rejected run_id=%s message_id=%s error=%q", runID, message.ID, err.Error())
	}
}
