package contextcompaction

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"agentflow-platform/apps/api/internal/contextassembly"
	"agentflow-platform/apps/api/internal/domain"
	eventpkg "agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/failure"
)

var ErrSummaryUnavailable = failure.New(failure.Definition{
	Message: "context compaction summary is unavailable",
	Info: failure.Info{
		Code: "context_summary_unavailable", Source: "context_compaction",
		Category: failure.CategoryAvailability, Retryable: true,
	},
})

const AlgorithmVersion = "context-compaction-v2"

type Store interface {
	ListMessages(conversationID string) ([]domain.Message, error)
	CommitContextCompaction(domain.ContextCompaction, domain.RunEvent) (domain.ContextCompaction, domain.RunEvent, error)
	GetLatestContextCompaction(conversationID string) (domain.ContextCompaction, bool, error)
	CreateRunEvent(domain.RunEvent) (domain.RunEvent, error)
	ListConversationRunEvents(conversationID string) ([]domain.RunEvent, error)
}

type SummaryRequest struct {
	SystemPrompt string
	Prompt       string
	TargetTokens int
}

type SummaryResult struct {
	Text  string
	Model string
}

type Summarizer interface {
	Summarize(context.Context, SummaryRequest) (SummaryResult, error)
}

type SummarizerFunc func(context.Context, SummaryRequest) (SummaryResult, error)

func (f SummarizerFunc) Summarize(ctx context.Context, request SummaryRequest) (SummaryResult, error) {
	return f(ctx, request)
}

type Request struct {
	RunID          string
	ConversationID string
	Trigger        string
	// TriggerKey identifies one logical model input. Overflow recovery for the
	// same key is attempted at most once; a new Turn supplies a new key.
	TriggerKey           string
	ObservedPromptTokens int
	Config               domain.ContextAssemblyConfig
	Summarizer           Summarizer
}

type Compactor struct {
	store Store
	now   func() time.Time
	mu    sync.Mutex
	locks map[string]*sync.Mutex
	state map[string]policyState
}

func NewCompactor(store Store) *Compactor {
	return &Compactor{
		store: store, now: func() time.Time { return time.Now().UTC() },
		locks: map[string]*sync.Mutex{}, state: map[string]policyState{},
	}
}

func (s *Compactor) CompactIfNeeded(ctx context.Context, request Request) (*domain.ContextCompaction, error) {
	if s == nil || s.store == nil {
		return nil, errors.New("context compaction store is required")
	}
	config := contextassembly.NormalizeConfig(request.Config)
	if config.CompactionMode == contextassembly.CompactionModeOff {
		return nil, nil
	}
	lock := s.conversationLock(request.ConversationID)
	lock.Lock()
	defer lock.Unlock()

	if err := s.reconcileOrphans(request.ConversationID, time.Duration(config.CompactionTimeoutMS)*time.Millisecond); err != nil {
		return nil, fmt.Errorf("reconcile orphan context compactions: %w", err)
	}
	messages, err := s.store.ListMessages(request.ConversationID)
	if err != nil {
		return nil, err
	}
	latest, hasPrevious, err := s.store.GetLatestContextCompaction(request.ConversationID)
	if err != nil {
		return nil, err
	}
	var previous *domain.ContextCompaction
	if hasPrevious {
		previous = &latest
		s.seedPolicyFromCompaction(latest)
	}
	plan := buildPlan(messages, previous, config)
	triggerTokens := max(plan.beforeTokens, request.ObservedPromptTokens)
	if triggerTokens < thresholdTokens(config, request.Trigger) || len(plan.newSourceMessages) < 2 {
		return nil, nil
	}
	sourceHash := sourceHash(messages, plan.sourceIDs)
	if !s.allowAttempt(request, sourceHash) {
		return nil, nil
	}

	compactionID := newCompactionID()
	generation := int64(1)
	previousID := ""
	if previous != nil {
		generation = max(int64(1), previous.Generation+1)
		previousID = previous.ID
	}
	replacementSummaryID := "summary:" + compactionID
	targetTokens := targetSummaryTokens(plan.beforeTokens-plan.protectedTokens, config.CompactionSummaryMaxTokens)
	startedPayload := eventpkg.ContextCompactionPayload{
		CompactionID: compactionID, Generation: generation, PreviousCompactionID: previousID,
		ReplacementSummaryID: replacementSummaryID, Trigger: request.Trigger, TriggerKey: request.TriggerKey,
		Status: "running", SourceMessageIDs: plan.sourceIDs, SourceEventIDs: []string{},
		ShadowedMessageRange: plan.shadowedRange, BeforeTokens: plan.beforeTokens,
		TargetSummaryTokens: targetTokens, ObservedPromptTokens: request.ObservedPromptTokens,
		AlgorithmVersion: AlgorithmVersion,
	}
	if _, err := s.publish(request, domain.EventCompactionStarted, startedPayload, nil); err != nil {
		return nil, fmt.Errorf("publish context compaction start: %w", err)
	}
	s.markAttempt(request, sourceHash)

	if request.Summarizer == nil {
		return nil, s.failAttempt(request, startedPayload, ErrSummaryUnavailable, true)
	}
	result, err := request.Summarizer.Summarize(ctx, SummaryRequest{
		SystemPrompt: compactionSystemPrompt,
		Prompt:       compactionPrompt(previous, plan.newSourceMessages, targetTokens),
		TargetTokens: targetTokens,
	})
	if err != nil || strings.TrimSpace(result.Text) == "" {
		if err == nil {
			err = ErrSummaryUnavailable
		}
		startedPayload.SummaryModel = result.Model
		return nil, s.failAttempt(request, startedPayload, err, false)
	}

	summary := limitSummary(strings.TrimSpace(result.Text), targetTokens)
	afterTokens := contextassembly.EstimateTokens(summary) + plan.protectedTokens
	reductionRatio := tokenReductionRatio(plan.beforeTokens, afterTokens)
	outcome := s.evaluateOutcome(request.ConversationID, reductionRatio)
	now := s.now()
	compaction := domain.ContextCompaction{
		ID: compactionID, ConversationID: request.ConversationID, RunID: request.RunID,
		Trigger: request.Trigger, Status: domain.ContextCompactionCompleted, Generation: generation,
		PreviousCompactionID: previousID, ReplacementSummaryID: replacementSummaryID,
		Summary: summary, SourceMessageIDs: plan.sourceIDs, SourceEventIDs: []string{},
		ShadowedMessageRange: plan.shadowedRange, SourceHash: sourceHash,
		BeforeTokens: plan.beforeTokens, AfterTokens: afterTokens, TargetSummaryTokens: targetTokens,
		ReductionRatio: reductionRatio, ConsecutiveLowYield: outcome.consecutiveLowYield,
		SummaryModel: result.Model, AlgorithmVersion: AlgorithmVersion,
		SurfaceReplacedAt: &now, CreatedAt: now,
	}
	completedPayload := payloadFromCompaction(compaction, request)
	completedPayload.Status = "completed"
	completedPayload.CooldownReason = outcome.cooldownReason
	if !outcome.cooldownUntil.IsZero() {
		completedPayload.CooldownUntil = &outcome.cooldownUntil
	}
	completionEvent, err := s.newEvent(request, domain.EventCompactionCompleted, completedPayload, nil)
	if err != nil {
		return nil, s.failAttempt(request, startedPayload, err, false)
	}
	created, _, err := s.store.CommitContextCompaction(compaction, completionEvent)
	if err != nil {
		return nil, s.failAttempt(request, startedPayload, err, false)
	}
	if created.ID != compactionID {
		duplicateErr := fmt.Errorf("compaction source already committed by %s", created.ID)
		if _, publishErr := s.publish(request, domain.EventCompactionFailed, failedPayload(startedPayload, duplicateErr, "concurrent_commit", nil), duplicateErr); publishErr != nil {
			return nil, errors.Join(duplicateErr, publishErr)
		}
		return &created, nil
	}
	s.applyOutcome(request.ConversationID, outcome)
	return &created, nil
}

func payloadFromCompaction(compaction domain.ContextCompaction, request Request) eventpkg.ContextCompactionPayload {
	return eventpkg.ContextCompactionPayload{
		CompactionID: compaction.ID, Generation: compaction.Generation,
		PreviousCompactionID: compaction.PreviousCompactionID,
		ReplacementSummaryID: compaction.ReplacementSummaryID,
		Trigger:              request.Trigger, TriggerKey: request.TriggerKey,
		SourceMessageIDs: compaction.SourceMessageIDs, SourceEventIDs: compaction.SourceEventIDs,
		ShadowedMessageRange: compaction.ShadowedMessageRange,
		BeforeTokens:         compaction.BeforeTokens, AfterTokens: compaction.AfterTokens,
		TargetSummaryTokens: compaction.TargetSummaryTokens, ReductionRatio: compaction.ReductionRatio,
		ConsecutiveLowYield:  compaction.ConsecutiveLowYield,
		ObservedPromptTokens: request.ObservedPromptTokens, SummaryModel: compaction.SummaryModel,
		AlgorithmVersion: compaction.AlgorithmVersion,
	}
}

func (s *Compactor) failAttempt(request Request, base eventpkg.ContextCompactionPayload, cause error, configuration bool) error {
	reason, until := s.recordFailure(request.ConversationID, cause, configuration)
	payload := failedPayload(base, cause, reason, &until)
	_, publishErr := s.publish(request, domain.EventCompactionFailed, payload, cause)
	if publishErr != nil {
		return errors.Join(cause, fmt.Errorf("publish context compaction failure: %w", publishErr))
	}
	return cause
}

func failedPayload(base eventpkg.ContextCompactionPayload, cause error, reason string, until *time.Time) eventpkg.ContextCompactionPayload {
	base.Status = "failed"
	base.Error = truncateError(cause.Error())
	base.CooldownReason = reason
	base.CooldownUntil = until
	return base
}

func (s *Compactor) publish(request Request, eventType domain.RunEventType, payload eventpkg.ContextCompactionPayload, cause error) (domain.RunEvent, error) {
	event, err := s.newEvent(request, eventType, payload, cause)
	if err != nil {
		return domain.RunEvent{}, err
	}
	return s.store.CreateRunEvent(event)
}

func (s *Compactor) newEvent(request Request, eventType domain.RunEventType, payload eventpkg.ContextCompactionPayload, cause error) (domain.RunEvent, error) {
	if request.RunID == "" {
		return domain.RunEvent{}, errors.New("context compaction run id is required")
	}
	encoded, err := eventpkg.Payload(payload)
	if err != nil {
		return domain.RunEvent{}, err
	}
	return domain.RunEvent{
		Type: eventType, RunID: request.RunID, ConversationID: request.ConversationID,
		Payload: failure.Merge(encoded, cause), Timestamp: s.now(),
	}, nil
}

func (s *Compactor) conversationLock(conversationID string) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	if lock := s.locks[conversationID]; lock != nil {
		return lock
	}
	lock := &sync.Mutex{}
	s.locks[conversationID] = lock
	return lock
}

func truncateError(value string) string {
	const limit = 1000
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "...[truncated]"
}
