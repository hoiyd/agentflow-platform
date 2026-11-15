package agent

import (
	"agentflow-platform/apps/api/internal/agent/turn"
	"agentflow-platform/apps/api/internal/domain"
)

// Only Single forwards resumed Turn deltas directly to the Chat answer.
// Staged modes retain their own final-answer selection and delivery.
func resumedTurnEvents(runID, mode string, events chan<- domain.RunEvent) turn.EventHandler {
	return func(item turn.Event) {
		forwardReasoning(events)(item)
		if mode == ChatModeSingle && item.Type == turn.EventModelDelta {
			event := liveDeltaEvent(runID, item.Delta)
			if item.Reset {
				event.Payload["reset"] = true
			}
			events <- event
		}
	}
}

func forwardResumedEvents(resumed <-chan domain.RunEvent, failures <-chan error, events chan<- domain.RunEvent) error {
	for item := range resumed {
		events <- item
	}
	return <-failures
}
