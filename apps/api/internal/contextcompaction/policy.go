package contextcompaction

import (
	"time"

	"agentflow-platform/apps/api/internal/contextassembly"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/failure"
)

const (
	minimumUsefulReduction = 0.10
	transientCooldown      = time.Minute
	configurationCooldown  = 15 * time.Minute
	lowYieldCooldown       = 30 * time.Minute
)

type policyState struct {
	consecutiveLowYield int
	cooldownUntil       time.Time
	cooldownReason      string
	overflowTriggerKey  string
}

type compactionOutcome struct {
	consecutiveLowYield int
	cooldownReason      string
	cooldownUntil       time.Time
}

func (s *Compactor) seedPolicyFromCompaction(compaction domain.ContextCompaction) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.state[compaction.ConversationID]
	if compaction.ConsecutiveLowYield > state.consecutiveLowYield {
		state.consecutiveLowYield = compaction.ConsecutiveLowYield
	}
	s.state[compaction.ConversationID] = state
}

func (s *Compactor) allowAttempt(request Request, sourceHash string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.state[request.ConversationID]
	if s.now().Before(state.cooldownUntil) {
		return false
	}
	if !state.cooldownUntil.IsZero() {
		if state.cooldownReason == "low_yield" {
			state.consecutiveLowYield = 0
		}
		state.cooldownReason = ""
		state.cooldownUntil = time.Time{}
		s.state[request.ConversationID] = state
	}
	if request.Trigger == contextassembly.CompactionTriggerOverflow {
		key := request.TriggerKey
		if key == "" {
			key = sourceHash
		}
		if state.overflowTriggerKey == key {
			return false
		}
	}
	return true
}

func (s *Compactor) markAttempt(request Request, sourceHash string) {
	if request.Trigger != contextassembly.CompactionTriggerOverflow {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.state[request.ConversationID]
	state.overflowTriggerKey = request.TriggerKey
	if state.overflowTriggerKey == "" {
		state.overflowTriggerKey = sourceHash
	}
	s.state[request.ConversationID] = state
}

func (s *Compactor) recordOutcome(conversationID string, reductionRatio float64) int {
	outcome := s.evaluateOutcome(conversationID, reductionRatio)
	s.applyOutcome(conversationID, outcome)
	return outcome.consecutiveLowYield
}

func (s *Compactor) evaluateOutcome(conversationID string, reductionRatio float64) compactionOutcome {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.state[conversationID]
	if reductionRatio < minimumUsefulReduction {
		state.consecutiveLowYield++
	} else {
		state.consecutiveLowYield = 0
		if state.cooldownReason == "low_yield" {
			state.cooldownReason = ""
			state.cooldownUntil = time.Time{}
		}
	}
	if state.consecutiveLowYield >= 2 {
		state.cooldownReason = "low_yield"
		state.cooldownUntil = s.now().Add(lowYieldCooldown)
	}
	return compactionOutcome{
		consecutiveLowYield: state.consecutiveLowYield,
		cooldownReason:      state.cooldownReason, cooldownUntil: state.cooldownUntil,
	}
}

func (s *Compactor) applyOutcome(conversationID string, outcome compactionOutcome) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.state[conversationID]
	state.consecutiveLowYield = outcome.consecutiveLowYield
	if outcome.cooldownReason == "" && state.cooldownReason == "low_yield" {
		state.cooldownReason = ""
		state.cooldownUntil = time.Time{}
	} else if outcome.cooldownReason != "" {
		state.cooldownReason = outcome.cooldownReason
		state.cooldownUntil = outcome.cooldownUntil
	}
	s.state[conversationID] = state
}

func (s *Compactor) recordFailure(conversationID string, err error, configuration bool) (string, time.Time) {
	reason := "summarizer_transient_failure"
	duration := transientCooldown
	info := failure.Describe(err)
	if configuration || info.Category == failure.CategoryAuthentication || info.Category == failure.CategoryQuota ||
		info.Category == failure.CategoryValidation || info.Category == failure.CategoryNotFound {
		reason = "summarizer_configuration_failure"
		duration = configurationCooldown
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.state[conversationID]
	state.cooldownReason = reason
	state.cooldownUntil = s.now().Add(duration)
	s.state[conversationID] = state
	return reason, state.cooldownUntil
}

func (s *Compactor) currentCooldown(conversationID string) (string, *time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.state[conversationID]
	if state.cooldownReason == "" || state.cooldownUntil.IsZero() {
		return "", nil
	}
	until := state.cooldownUntil
	return state.cooldownReason, &until
}

func (s *Compactor) hydrateCooldown(conversationID, reason string, until time.Time) {
	if reason == "" || until.IsZero() {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.state[conversationID]
	if until.After(state.cooldownUntil) {
		state.cooldownReason = reason
		state.cooldownUntil = until
		s.state[conversationID] = state
	}
}
