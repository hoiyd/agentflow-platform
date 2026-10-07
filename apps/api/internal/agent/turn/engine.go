package turn

import (
	"context"
	"errors"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/budget"
	"agentflow-platform/apps/api/internal/domain"
	eventpkg "agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/failure"
)

type Engine struct{ model Model }

func NewEngine(model Model) *Engine { return &Engine{model: model} }

func (e *Engine) Execute(ctx context.Context, request Request, handler EventHandler) (Result, error) {
	if err := request.Validate(); err != nil {
		return Result{}, err
	}
	if e == nil || e.model == nil {
		return Result{}, errors.New("turn model is required")
	}
	if request.TurnID == "" {
		request.TurnID = "turn_" + time.Now().UTC().Format("20060102150405.000000000")
	}
	modelCtx, cancelModel := context.WithCancel(ctx)
	defer cancelModel()
	var output *eventpkg.OutputRecorder
	if request.Sink != nil {
		output = eventpkg.NewOutputRecorder(request.Sink, cancelModel)
		request.Sink = output
		defer output.Close()
	}
	var sinkErr error
	publish := func(item Event) {
		item.TurnID, item.ConversationID = request.TurnID, request.ConversationID
		emit(handler, item)
		if request.Sink == nil {
			return
		}
		payload := map[string]any{}
		if item.ModelCallID != "" {
			payload["model_call_id"] = item.ModelCallID
		}
		if item.Attempt > 0 {
			payload["attempt"] = item.Attempt
		}
		eventType := unifiedEventType(item.Type)
		if item.Reasoning != nil {
			var err error
			payload, err = eventpkg.Payload(*item.Reasoning)
			eventType = item.Reasoning.EventType()
			if err != nil {
				sinkErr = err
				return
			}
		}
		payload["role"] = request.Role
		if item.Type == EventTurnStarted {
			payload["agent_id"] = request.Agent.ID
		}
		if item.Delta != "" {
			payload["delta"] = item.Delta
		}
		if item.DisplayText != nil {
			payload["display_text"] = *item.DisplayText
			payload["display_truncated"] = item.DisplayTruncated
		}
		if item.Reset {
			payload["reset"] = true
		}
		if item.ToolName != "" {
			payload["tool_name"] = item.ToolName
		}
		if item.ToolCallID != "" {
			payload["tool_call_id"] = item.ToolCallID
		}
		if item.Error != "" {
			payload["error"] = item.Error
		}
		if item.Cause != nil {
			payload = failure.Merge(payload, item.Cause)
		}
		if item.Result != nil {
			payload["output"] = item.Result.Output
			payload["stop_reason"] = item.Result.StopReason
			payload["model"] = item.Result.Usage.Model
			payload["prompt_tokens"] = item.Result.Usage.PromptTokens
			payload["completion_tokens"] = item.Result.Usage.CompletionTokens
			payload["total_tokens"] = item.Result.Usage.TotalTokens
			payload["token_usage_estimated"] = item.Result.Usage.Estimated
		}
		if err := request.Sink.Publish(ctx, domain.RunEvent{Type: eventType, RunID: request.RunID,
			ConversationID: request.ConversationID, StageID: request.StepID, TurnID: request.TurnID,
			Payload: payload, Timestamp: time.Now().UTC()}); err != nil && sinkErr == nil {
			sinkErr = err
		}
	}
	base := Event{RunID: request.RunID, StepID: request.StepID}
	publish(withType(base, EventTurnStarted))
	emit(handler, withType(base, EventModelStarted))
	if sinkErr != nil {
		return Result{}, sinkErr
	}
	modelCtx = eventpkg.WithScope(modelCtx, eventpkg.Scope{ConversationID: request.ConversationID, RunID: request.RunID, StageID: request.StepID, TurnID: request.TurnID})
	modelCtx = budget.WithScope(modelCtx, budget.Scope{StageID: request.StepID, TurnID: request.TurnID})
	result, err := e.model.Execute(modelCtx, request, func(item ModelEvent) {
		t := item.Type
		if t == "" {
			t = EventModelDelta
		}
		publish(Event{Type: t, RunID: request.RunID, StepID: request.StepID, Delta: item.Delta, Reset: item.Reset,
			DisplayText: item.DisplayText, DisplayTruncated: item.DisplayTruncated,
			ModelCallID: item.ModelCallID, Attempt: item.Attempt,
			ToolName: item.ToolName, ToolCallID: item.ToolCallID, Error: item.Error, Reasoning: item.Reasoning})
	})
	if output != nil {
		status := "final"
		if err != nil {
			status = "interrupted"
		}
		if flushErr := output.Flush(status); flushErr != nil && sinkErr == nil {
			sinkErr = flushErr
		}
	}
	if sinkErr != nil {
		return result, sinkErr
	}
	if err != nil {
		result.StopReason = StopFailed
		if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			result.StopReason = StopCanceled
		}
		failed := Event{RunID: request.RunID, StepID: request.StepID, Result: &result, Error: err.Error(), Cause: err}
		failed.Type = EventModelFailed
		emit(handler, failed)
		failed.Type = EventTurnFailed
		publish(failed)
		return result, err
	}
	result.Output = strings.TrimSpace(result.Output)
	if result.StopReason == "" {
		result.StopReason = StopCompleted
	}
	emit(handler, Event{Type: EventModelFinished, RunID: request.RunID, StepID: request.StepID, Result: &result})
	publish(Event{Type: EventTurnCompleted, RunID: request.RunID, StepID: request.StepID, Result: &result})
	return result, sinkErr
}

func unifiedEventType(value EventType) domain.RunEventType {
	switch value {
	case EventTurnStarted:
		return domain.EventTurnStarted
	case EventTurnCompleted:
		return domain.EventTurnCompleted
	case EventTurnFailed:
		return domain.EventTurnFailed
	case EventModelStarted:
		return domain.EventModelStarted
	case EventModelDelta:
		return domain.EventModelDelta
	case EventModelReasoning:
		return domain.EventModelReasoning
	case EventModelFinished:
		return domain.EventModelCompleted
	case EventModelFailed:
		return domain.EventModelFailed
	case EventToolStarted:
		return domain.EventToolStarted
	case EventToolFinished:
		return domain.EventToolCompleted
	case EventToolFailed:
		return domain.EventToolFailed
	default:
		return domain.RunEventType(value)
	}
}
func withType(item Event, eventType EventType) Event { item.Type = eventType; return item }
