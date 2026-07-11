package turn

import (
	"context"
	"errors"
	"strings"
)

type Engine struct {
	model Model
}

func NewEngine(model Model) *Engine {
	return &Engine{model: model}
}

func (e *Engine) Execute(ctx context.Context, request Request, handler EventHandler) (Result, error) {
	if err := request.Validate(); err != nil {
		return Result{}, err
	}
	if e == nil || e.model == nil {
		return Result{}, errors.New("turn model is required")
	}

	base := Event{RunID: request.RunID, StepID: request.StepID}
	emit(handler, withType(base, EventTurnStarted))
	emit(handler, withType(base, EventModelStarted))

	result, err := e.model.Execute(ctx, request, func(event ModelEvent) {
		emit(handler, Event{Type: EventModelDelta, RunID: request.RunID, StepID: request.StepID, Delta: event.Delta})
	})
	if err != nil {
		result.StopReason = StopFailed
		if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			result.StopReason = StopCanceled
		}
		emit(handler, Event{Type: EventTurnFailed, RunID: request.RunID, StepID: request.StepID, Result: &result, Error: err.Error()})
		return result, err
	}

	result.Output = strings.TrimSpace(result.Output)
	if result.StopReason == "" {
		result.StopReason = StopCompleted
	}
	emit(handler, Event{Type: EventModelFinished, RunID: request.RunID, StepID: request.StepID, Result: &result})
	emit(handler, Event{Type: EventTurnCompleted, RunID: request.RunID, StepID: request.StepID, Result: &result})
	return result, nil
}

func withType(event Event, eventType EventType) Event {
	event.Type = eventType
	return event
}
