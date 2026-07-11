package turn

import "time"

type EventType string

const (
	EventTurnStarted   EventType = "turn.started"
	EventModelStarted  EventType = "model.started"
	EventModelDelta    EventType = "model.delta"
	EventModelFinished EventType = "model.finished"
	EventTurnCompleted EventType = "turn.completed"
	EventTurnFailed    EventType = "turn.failed"
)

type Event struct {
	Type      EventType
	RunID     string
	StepID    string
	Delta     string
	Result    *Result
	Error     string
	Timestamp time.Time
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
