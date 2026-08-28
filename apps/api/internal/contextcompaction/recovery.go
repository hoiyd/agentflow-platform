package contextcompaction

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	eventpkg "agentflow-platform/apps/api/internal/event"
)

const minimumOrphanGracePeriod = time.Minute

// ReconcileOrphans closes stale started events that never reached the atomic
// summary-surface commit. Because a completed row and terminal event are written
// together, an unmatched start cannot represent a successful compaction.
func (s *Compactor) ReconcileOrphans(conversationID string, timeout time.Duration) error {
	if s == nil || s.store == nil {
		return errors.New("context compaction store is required")
	}
	lock := s.conversationLock(conversationID)
	lock.Lock()
	defer lock.Unlock()
	return s.reconcileOrphans(conversationID, timeout)
}

func (s *Compactor) reconcileOrphans(conversationID string, timeout time.Duration) error {
	events, err := s.store.ListConversationRunEvents(conversationID)
	if err != nil {
		return err
	}
	s.hydratePolicyFromEvents(conversationID, events)
	terminal := map[string]bool{}
	for _, runEvent := range events {
		if runEvent.Type != domain.EventCompactionCompleted && runEvent.Type != domain.EventCompactionFailed {
			continue
		}
		if id, _ := runEvent.Payload["compaction_id"].(string); id != "" {
			terminal[id] = true
		}
	}
	grace := max(minimumOrphanGracePeriod, timeout*2)
	cutoff := s.now().Add(-grace)
	for _, started := range events {
		if started.Type != domain.EventCompactionStarted || started.Timestamp.After(cutoff) {
			continue
		}
		compactionID, _ := started.Payload["compaction_id"].(string)
		if compactionID == "" || terminal[compactionID] {
			continue
		}
		payload := eventpkg.ContextCompactionPayload{
			CompactionID: compactionID, Trigger: stringValue(started.Payload["trigger"]),
			Status: "failed", AlgorithmVersion: stringValue(started.Payload["algorithm_version"]),
			RecoveredFromOrphan: true, CooldownReason: "orphaned_attempt",
			Error: "compaction stopped before the summary surface was atomically committed",
		}
		encoded, encodeErr := eventpkg.Payload(payload)
		if encodeErr != nil {
			return encodeErr
		}
		if _, createErr := s.store.CreateRunEvent(domain.RunEvent{
			Type: domain.EventCompactionFailed, RunID: started.RunID,
			ConversationID: conversationID, Payload: encoded, Timestamp: s.now(),
		}); createErr != nil {
			return fmt.Errorf("close orphan compaction %s: %w", compactionID, createErr)
		}
		terminal[compactionID] = true
	}
	return nil
}

func (s *Compactor) hydratePolicyFromEvents(conversationID string, events []domain.RunEvent) {
	overflowKey := ""
	for index := len(events) - 1; index >= 0; index-- {
		if overflowKey == "" && stringValue(events[index].Payload["trigger"]) == "provider_overflow" {
			overflowKey = stringValue(events[index].Payload["trigger_key"])
		}
		reason := stringValue(events[index].Payload["cooldown_reason"])
		until, ok := timeValue(events[index].Payload["cooldown_until"])
		if reason == "" || !ok {
			continue
		}
		s.hydrateCooldown(conversationID, reason, until)
		break
	}
	if overflowKey != "" {
		s.mu.Lock()
		state := s.state[conversationID]
		if state.overflowTriggerKey == "" {
			state.overflowTriggerKey = overflowKey
			s.state[conversationID] = state
		}
		s.mu.Unlock()
	}
}

func timeValue(value any) (time.Time, bool) {
	switch typed := value.(type) {
	case time.Time:
		return typed, !typed.IsZero()
	case *time.Time:
		if typed != nil {
			return *typed, !typed.IsZero()
		}
	case string:
		parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(typed))
		return parsed, err == nil
	}
	return time.Time{}, false
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}
