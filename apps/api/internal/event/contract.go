package event

import (
	"fmt"
	"sort"

	"agentflow-platform/apps/api/internal/domain"
)

// ValidateLifecycle checks the durable ordering and pairing invariants shared
// by Single, Multi-Agent, Autonomous, and Replay event streams.
func ValidateLifecycle(events []domain.RunEvent) error {
	state, err := scanLifecycle(events)
	if err != nil {
		return err
	}
	if item, ok := firstOpen(state.stages); ok {
		return fmt.Errorf("stage %s has no terminal event", item.StageID)
	}
	if item, ok := firstOpen(state.turns); ok {
		return fmt.Errorf("turn %s has no terminal event", item.TurnID)
	}
	if item, ok := firstOpen(state.models); ok {
		return fmt.Errorf("model call in turn %s has no terminal event", item.TurnID)
	}
	if item, ok := firstOpen(state.tools); ok {
		return fmt.Errorf("tool call %s has no terminal event", toolCallID(item))
	}
	return nil
}

type lifecycleState struct {
	stages map[string]domain.RunEvent
	turns  map[string]domain.RunEvent
	models map[string]domain.RunEvent
	tools  map[string]domain.RunEvent
}

func scanLifecycle(events []domain.RunEvent) (lifecycleState, error) {
	strictStagePairing := false
	for _, item := range events {
		if item.Type == domain.EventCheckpointCaptured || item.Type == domain.EventCheckpointRestored || item.Type == domain.EventCheckpointStale {
			strictStagePairing = true
			break
		}
	}
	state := lifecycleState{
		stages: map[string]domain.RunEvent{}, turns: map[string]domain.RunEvent{},
		models: map[string]domain.RunEvent{}, tools: map[string]domain.RunEvent{},
	}
	for index, item := range events {
		want := int64(index + 1)
		if item.Sequence != want {
			return state, newProtocolViolation("event_sequence_gap", item, fmt.Sprintf("event sequence %d: got %d", index, item.Sequence))
		}
		if err := validateRegisteredEvent(item); err != nil {
			return state, err
		}
		switch item.Type {
		case domain.EventStageStarted:
			if item.StageID == "" {
				return state, newProtocolViolation("stage_scope_missing", item, fmt.Sprintf("invalid stage start at sequence %d", item.Sequence))
			}
			if _, exists := state.stages[item.StageID]; exists {
				return state, newProtocolViolation("stage_overlap", item, fmt.Sprintf("overlapping stage %s at sequence %d", item.StageID, item.Sequence))
			}
			state.stages[item.StageID] = item
		case domain.EventStageCompleted, domain.EventStageFailed, domain.EventStageCanceled:
			if _, ok := state.stages[item.StageID]; !ok {
				if !strictStagePairing {
					continue
				}
				return state, newProtocolViolation("stage_terminal_orphan", item, fmt.Sprintf("unmatched stage terminal event at sequence %d", item.Sequence))
			}
			delete(state.stages, item.StageID)
		case domain.EventTurnStarted:
			if item.TurnID == "" {
				return state, newProtocolViolation("turn_scope_missing", item, fmt.Sprintf("invalid turn start at sequence %d", item.Sequence))
			}
			if _, exists := state.turns[item.TurnID]; exists {
				return state, newProtocolViolation("turn_overlap", item, fmt.Sprintf("overlapping turn at sequence %d", item.Sequence))
			}
			state.turns[item.TurnID] = item
		case domain.EventTurnCompleted, domain.EventTurnFailed, domain.EventTurnCanceled:
			if _, ok := state.turns[item.TurnID]; !ok {
				return state, newProtocolViolation("turn_terminal_orphan", item, fmt.Sprintf("unmatched turn terminal event at sequence %d", item.Sequence))
			}
			delete(state.turns, item.TurnID)
		case domain.EventModelStarted:
			if item.TurnID == "" {
				return state, newProtocolViolation("model_scope_missing", item, fmt.Sprintf("invalid model start at sequence %d", item.Sequence))
			}
			if _, exists := state.models[item.TurnID]; exists {
				return state, newProtocolViolation("model_overlap", item, fmt.Sprintf("overlapping model call at sequence %d", item.Sequence))
			}
			state.models[item.TurnID] = item
		case domain.EventModelCompleted, domain.EventModelFailed:
			if _, ok := state.models[item.TurnID]; !ok {
				return state, newProtocolViolation("model_terminal_orphan", item, fmt.Sprintf("unmatched model terminal event at sequence %d", item.Sequence))
			}
			delete(state.models, item.TurnID)
		case domain.EventToolStarted:
			id := toolCallID(item)
			if id == "" {
				return state, newProtocolViolation("tool_call_id_missing", item, fmt.Sprintf("invalid tool start at sequence %d", item.Sequence))
			}
			if _, exists := state.tools[id]; exists {
				return state, newProtocolViolation("tool_overlap", item, fmt.Sprintf("overlapping tool call at sequence %d", item.Sequence))
			}
			state.tools[id] = item
		case domain.EventToolCompleted, domain.EventToolFailed:
			id := toolCallID(item)
			if _, ok := state.tools[id]; !ok {
				return state, newProtocolViolation("tool_terminal_orphan", item, fmt.Sprintf("unmatched tool terminal event at sequence %d", item.Sequence))
			}
			delete(state.tools, id)
		}
	}
	return state, nil
}

func firstOpen(items map[string]domain.RunEvent) (domain.RunEvent, bool) {
	var first domain.RunEvent
	found := false
	for _, item := range items {
		if !found || item.Sequence < first.Sequence || (item.Sequence == first.Sequence && item.ID < first.ID) {
			first = item
			found = true
		}
	}
	return first, found
}

func toolCallID(item domain.RunEvent) string {
	id, _ := item.Payload["tool_call_id"].(string)
	return id
}

// PlanInterruptedLifecycleRepair closes only scopes left open by a crashed
// worker. Inner scopes are terminated before their parents.
func PlanInterruptedLifecycleRepair(events []domain.RunEvent, reason string) ([]domain.RunEvent, error) {
	state, err := scanLifecycle(events)
	if err != nil {
		return nil, err
	}
	cursor := int64(0)
	if len(events) > 0 {
		cursor = events[len(events)-1].Sequence
	}
	terminal := make([]domain.RunEvent, 0, len(state.tools)+len(state.models)+len(state.turns)+len(state.stages))
	appendOpen := func(items map[string]domain.RunEvent, terminalType domain.RunEventType) {
		open := make([]domain.RunEvent, 0, len(items))
		for _, item := range items {
			open = append(open, item)
		}
		sort.Slice(open, func(i, j int) bool { return open[i].Sequence > open[j].Sequence })
		for _, started := range open {
			cursor++
			payload := make(map[string]any, len(started.Payload)+3)
			for key, value := range started.Payload {
				payload[key] = value
			}
			payload["synthetic"] = true
			payload["reason"] = reason
			payload["repaired_from_event_id"] = started.ID
			terminal = append(terminal, domain.RunEvent{
				Type: terminalType, SchemaVersion: domain.CurrentRunEventSchemaVersion,
				Sequence: cursor, ConversationID: started.ConversationID, RunID: started.RunID,
				StageID: started.StageID, TurnID: started.TurnID, ParentEventID: started.ID,
				Payload: payload,
			})
		}
	}
	appendOpen(state.tools, domain.EventToolFailed)
	appendOpen(state.models, domain.EventModelFailed)
	appendOpen(state.turns, domain.EventTurnFailed)
	appendOpen(state.stages, domain.EventStageFailed)
	combined := append(append([]domain.RunEvent(nil), events...), terminal...)
	if err := ValidateLifecycle(combined); err != nil {
		return nil, fmt.Errorf("planned recovery is invalid: %w", err)
	}
	return terminal, nil
}
