package turn

import (
	eventpkg "agentflow-platform/apps/api/internal/event"
	"time"
)

type EventType string

const (
	EventTurnStarted    EventType = "turn.started"
	EventModelStarted   EventType = "model.started"
	EventModelDelta     EventType = "model.delta"
	EventModelReasoning EventType = "model.reasoning"
	EventModelFinished  EventType = "model.finished"
	EventModelFailed    EventType = "model.failed"
	EventToolStarted    EventType = "tool.started"
	EventToolFinished   EventType = "tool.finished"
	EventToolFailed     EventType = "tool.failed"
	EventTurnCompleted  EventType = "turn.completed"
	EventTurnFailed     EventType = "turn.failed"
)

type Event struct {
	Type             EventType
	RunID            string
	StepID           string
	TurnID           string
	ConversationID   string
	Reasoning        *eventpkg.ModelReasoningPayload
	Delta            string
	DisplayText      *string
	DisplayTruncated bool
	ModelCallID      string
	Attempt          int
	Reset            bool
	ToolName         string
	ToolCallID       string
	Result           *Result
	Error            string
	Cause            error
	Timestamp        time.Time
}

type EventHandler func(Event)

func emit(handler EventHandler, event Event) {
	if handler == nil {
		return
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	handler(event)
}
