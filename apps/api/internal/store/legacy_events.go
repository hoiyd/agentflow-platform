package store

import (
	"sort"
	"time"

	"agentflow-platform/apps/api/internal/domain"
)

type legacyTraceEvent struct {
	ID         string         `json:"id"`
	RunID      string         `json:"run_id"`
	StepID     string         `json:"step_id,omitempty"`
	Type       string         `json:"type"`
	Payload    map[string]any `json:"payload"`
	Timestamp  time.Time      `json:"timestamp"`
	DurationMS int64          `json:"duration_ms,omitempty"`
}

func (s *FileStore) migrateLegacyTraceEventsLocked() bool {
	if len(s.data.LegacyTraceEvents) == 0 {
		return false
	}
	existing := map[string]bool{}
	next := map[string]int64{}
	for _, item := range s.data.RunEvents {
		existing[item.RunID] = true
		if item.Sequence > next[item.RunID] {
			next[item.RunID] = item.Sequence
		}
	}
	sort.SliceStable(s.data.LegacyTraceEvents, func(i, j int) bool {
		return s.data.LegacyTraceEvents[i].Timestamp.Before(s.data.LegacyTraceEvents[j].Timestamp)
	})
	for _, item := range s.data.LegacyTraceEvents {
		next[item.RunID]++
		payload := item.Payload
		if payload == nil {
			payload = map[string]any{}
		}
		if item.DurationMS > 0 {
			payload["duration_ms"] = item.DurationMS
		}
		eventType := legacyRunEventType(item.Type)
		if existing[item.RunID] {
			eventType = domain.RunEventType("legacy.trace." + item.Type)
			payload["migrated_legacy"] = true
		}
		s.data.RunEvents = append(s.data.RunEvents, domain.RunEvent{ID: item.ID, RunID: item.RunID, StageID: item.StepID,
			Type: eventType, SchemaVersion: domain.CurrentRunEventSchemaVersion, Sequence: next[item.RunID], Payload: payload, Timestamp: item.Timestamp})
	}
	return true
}

func legacyRunEventType(value string) domain.RunEventType {
	switch value {
	case "llm_start":
		return domain.EventModelStarted
	case "llm_end":
		return domain.EventModelCompleted
	case "tool_start":
		return domain.EventToolStarted
	case "tool_end":
		return domain.EventToolCompleted
	case "retrieval":
		return domain.EventRetrievalCompleted
	case "error":
		return domain.EventModelFailed
	default:
		return domain.RunEventType("legacy." + value)
	}
}
