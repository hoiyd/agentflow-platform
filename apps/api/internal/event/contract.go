package event

import (
	"fmt"

	"agentflow-platform/apps/api/internal/domain"
)

// ValidateLifecycle checks the durable ordering and pairing invariants shared
// by Single, Multi-Agent, Autonomous, and Replay event streams.
func ValidateLifecycle(events []domain.RunEvent) error {
	turns := map[string]bool{}
	models := map[string]bool{}
	tools := map[string]bool{}
	for index, item := range events {
		want := int64(index + 1)
		if item.Sequence != want {
			return fmt.Errorf("event sequence %d: got %d", index, item.Sequence)
		}
		if item.SchemaVersion != domain.CurrentRunEventSchemaVersion {
			return fmt.Errorf("event %d has unsupported schema version %d", item.Sequence, item.SchemaVersion)
		}
		switch item.Type {
		case domain.EventTurnStarted:
			if item.TurnID == "" || turns[item.TurnID] {
				return fmt.Errorf("invalid turn start at sequence %d", item.Sequence)
			}
			turns[item.TurnID] = false
		case domain.EventTurnCompleted, domain.EventTurnFailed, domain.EventTurnCanceled:
			if _, ok := turns[item.TurnID]; !ok || turns[item.TurnID] {
				return fmt.Errorf("unmatched turn terminal event at sequence %d", item.Sequence)
			}
			turns[item.TurnID] = true
		case domain.EventModelStarted:
			if item.TurnID == "" {
				return fmt.Errorf("invalid model start at sequence %d", item.Sequence)
			}
			if done, exists := models[item.TurnID]; exists && !done {
				return fmt.Errorf("overlapping model call at sequence %d", item.Sequence)
			}
			models[item.TurnID] = false
		case domain.EventModelCompleted, domain.EventModelFailed:
			if _, ok := models[item.TurnID]; !ok || models[item.TurnID] {
				return fmt.Errorf("unmatched model terminal event at sequence %d", item.Sequence)
			}
			models[item.TurnID] = true
		case domain.EventToolStarted:
			id, _ := item.Payload["tool_call_id"].(string)
			if id == "" || tools[id] {
				return fmt.Errorf("invalid tool start at sequence %d", item.Sequence)
			}
			tools[id] = false
		case domain.EventToolCompleted, domain.EventToolFailed:
			id, _ := item.Payload["tool_call_id"].(string)
			if _, ok := tools[id]; !ok || tools[id] {
				return fmt.Errorf("unmatched tool terminal event at sequence %d", item.Sequence)
			}
			tools[id] = true
		}
	}
	for id, done := range turns {
		if !done {
			return fmt.Errorf("turn %s has no terminal event", id)
		}
	}
	for id, done := range models {
		if !done {
			return fmt.Errorf("model call in turn %s has no terminal event", id)
		}
	}
	for id, done := range tools {
		if !done {
			return fmt.Errorf("tool call %s has no terminal event", id)
		}
	}
	return nil
}
