package agent

import (
	turnpkg "agentflow-platform/apps/api/internal/agent/turn"
	"agentflow-platform/apps/api/internal/domain"
	eventpkg "agentflow-platform/apps/api/internal/event"
)

// Initiating Chat streams use the same sanitized envelope as live subscribers.
// The Turn sink publishes to the hub; this handler only forwards to the response.
func forwardReasoning(events chan<- domain.RunEvent) turnpkg.EventHandler {
	return func(item turnpkg.Event) {
		if item.Type != turnpkg.EventModelReasoning || item.Reasoning == nil {
			return
		}
		live, err := eventpkg.NewRunEvent(domain.EventModelReasoning, eventpkg.EventMetadata{
			RunID: item.RunID, ConversationID: item.ConversationID, StageID: item.StepID,
			TurnID: item.TurnID, Timestamp: item.Timestamp,
		}, *item.Reasoning)
		if err == nil && events != nil {
			events <- live
		}
	}
}
