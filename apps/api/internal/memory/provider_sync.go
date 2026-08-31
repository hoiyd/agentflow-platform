package memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	eventpkg "agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/failure"
)

const (
	AdaptiveModeOff    = "off"
	AdaptiveModeShadow = "shadow"
	AdaptiveModeAuto   = "auto"
)

type ProposalRequest struct {
	RunID          string
	IdempotencyKey string
	Message        domain.Message
}

type ProposalResult struct {
	Candidate domain.MemoryCandidate
	Proposed  bool
	Accepted  bool
	Duplicate bool
}

type TurnSyncRequest struct {
	RunID          string
	IdempotencyKey string
	Message        domain.Message
}

// SyncTurn is non-blocking. A single bounded worker preserves accepted order;
// the durable Candidate ID remains the idempotency authority after a job exits.
func (p *BuiltinProvider) SyncTurn(request TurnSyncRequest) error {
	if p == nil {
		return ErrProviderNotInitialized
	}
	if strings.TrimSpace(request.Message.Content) == "" || !strings.EqualFold(strings.TrimSpace(request.Message.Role), "user") {
		return nil
	}
	request.IdempotencyKey = turnSyncKey(request)
	p.mu.Lock()
	if p.state == providerCreated {
		p.mu.Unlock()
		return ErrProviderNotInitialized
	}
	if p.state != providerRunning {
		p.mu.Unlock()
		return ErrProviderClosed
	}
	if _, exists := p.inflight[request.IdempotencyKey]; exists {
		p.mu.Unlock()
		return nil
	}
	select {
	case p.jobs <- request:
		p.inflight[request.IdempotencyKey] = struct{}{}
		p.mu.Unlock()
		return nil
	default:
		p.mu.Unlock()
		p.publish(request.RunID, request.Message, domain.EventMemorySyncRejected, failure.Merge(map[string]any{
			"message_id": request.Message.ID, "role": request.Message.Role,
			"sync_key": request.IdempotencyKey, "error": ErrSyncQueueFull.Error(),
		}, ErrSyncQueueFull))
		return ErrSyncQueueFull
	}
}

func (p *BuiltinProvider) Propose(ctx context.Context, request ProposalRequest) (ProposalResult, error) {
	return p.propose(ctx, request, false)
}

func (p *BuiltinProvider) propose(ctx context.Context, request ProposalRequest, allowClosing bool) (ProposalResult, error) {
	if err := p.requireRunning(allowClosing); err != nil {
		return ProposalResult{}, err
	}
	request.RunID = strings.TrimSpace(request.RunID)
	request.IdempotencyKey = proposalKey(request)
	candidate := domain.MemoryCandidate{
		ID: candidateID(request.IdempotencyKey), ConversationID: request.Message.ConversationID,
		RunID: request.RunID, SourceMessageID: request.Message.ID, SourceRole: strings.TrimSpace(request.Message.Role),
	}

	var draft CandidateDraft
	var proposed bool
	if err := p.retry(ctx, "propose.extract", func() error {
		var err error
		draft, proposed, err = p.extractor.Extract(ctx, request.Message)
		return err
	}); err != nil {
		p.publish(request.RunID, request.Message, domain.EventMemoryCandidateFailed, candidatePayload(candidate, request.IdempotencyKey, "failed", err))
		return ProposalResult{}, err
	}
	if !proposed {
		return ProposalResult{}, nil
	}

	decision := p.policy.Evaluate(request.Message, draft)
	if decision.Accepted && draft.ExtractionReason == CandidateReasonAdaptive && p.adaptiveMode != AdaptiveModeAuto {
		decision = PolicyDecision{Reason: PolicyRejectShadowMode}
	}
	status := domain.MemoryCandidateRejected
	if decision.Accepted {
		status = domain.MemoryCandidateAccepted
	}
	content := draft.Content
	if decision.Reason == PolicyRejectSecret {
		content = "[redacted potential secret]"
	}
	candidate.Kind = draft.Kind
	candidate.Content = content
	candidate.Status = status
	candidate.ExtractionReason = draft.ExtractionReason
	candidate.PolicyReason = decision.Reason
	candidate.Confidence = draft.Confidence
	candidate.CreatedAt = time.Now().UTC()

	var stored domain.MemoryCandidate
	var created bool
	if err := p.retry(ctx, "propose.store", func() error {
		var err error
		stored, created, err = p.store.CreateMemoryCandidate(candidate)
		return err
	}); err != nil {
		p.publish(request.RunID, request.Message, domain.EventMemoryCandidateFailed, candidatePayload(candidate, request.IdempotencyKey, "failed", err))
		return ProposalResult{}, err
	}
	if !created {
		return ProposalResult{Candidate: stored, Proposed: true, Accepted: stored.Status == domain.MemoryCandidateAccepted, Duplicate: true}, nil
	}

	candidate = stored
	p.publish(request.RunID, request.Message, domain.EventMemoryCandidateProposed, candidatePayload(candidate, request.IdempotencyKey, "proposed", nil))
	if !decision.Accepted {
		p.publish(request.RunID, request.Message, domain.EventMemoryCandidateRejected, candidatePayload(candidate, request.IdempotencyKey, "rejected", nil))
		return ProposalResult{Candidate: candidate, Proposed: true}, nil
	}
	p.publish(request.RunID, request.Message, domain.EventMemoryCandidateAccepted, candidatePayload(candidate, request.IdempotencyKey, "accepted", nil))
	return ProposalResult{Candidate: candidate, Proposed: true, Accepted: true}, nil
}

func (p *BuiltinProvider) runSyncWorker() {
	defer func() {
		p.mu.Lock()
		p.state = providerClosed
		p.mu.Unlock()
		p.cancel()
		close(p.done)
	}()
	for request := range p.jobs {
		p.syncTurn(request)
		p.mu.Lock()
		delete(p.inflight, request.IdempotencyKey)
		p.mu.Unlock()
	}
}

func (p *BuiltinProvider) syncTurn(request TurnSyncRequest) {
	ctx, cancel := context.WithTimeout(p.ctx, p.options.JobTimeout)
	defer cancel()
	proposal, err := p.propose(ctx, ProposalRequest{
		RunID: request.RunID, IdempotencyKey: request.IdempotencyKey, Message: request.Message,
	}, true)
	if err != nil || !proposal.Proposed || !proposal.Accepted || proposal.Duplicate {
		return
	}
	p.commitCandidate(ctx, request, proposal.Candidate)
}

func (p *BuiltinProvider) commitCandidate(ctx context.Context, request TurnSyncRequest, candidate domain.MemoryCandidate) {
	startedAt := time.Now()
	payload := map[string]any{
		"candidate_id": candidate.ID, "message_id": request.Message.ID,
		"role": request.Message.Role, "kind": candidate.Kind,
		"source": "builtin_memory_provider", "sync_key": request.IdempotencyKey,
	}
	p.publish(request.RunID, request.Message, domain.EventMemorySyncRequested, payload)

	memoryID := "mem_curated_" + candidate.ID
	created, err := p.commit(ctx, domain.Memory{
		ID: memoryID, WorkspaceID: request.Message.WorkspaceID,
		ConversationID: candidate.ConversationID, RunID: candidate.RunID,
		SourceMessageID: candidate.SourceMessageID, Kind: candidate.Kind, Content: candidate.Content,
		Metadata: map[string]any{
			"candidate_id": candidate.ID, "source_role": candidate.SourceRole,
			"extraction_reason": candidate.ExtractionReason, "sync_key": request.IdempotencyKey,
		},
		CreatedAt: candidate.CreatedAt,
	}, true)
	if err != nil {
		failed := clonePayload(payload)
		failed["error"] = err.Error()
		failed["duration_ms"] = time.Since(startedAt).Milliseconds()
		p.publish(request.RunID, request.Message, domain.EventMemorySyncFailed, failure.Merge(failed, err))
		log.Printf("memory_provider_sync_failed run_id=%s candidate_id=%s error=%q", request.RunID, candidate.ID, err.Error())
		return
	}

	completed := clonePayload(payload)
	completed["memory_id"] = created.ID
	completed["duration_ms"] = time.Since(startedAt).Milliseconds()
	p.publish(request.RunID, request.Message, domain.EventMemorySyncCompleted, completed)
}

func (p *BuiltinProvider) publish(runID string, message domain.Message, eventType domain.RunEventType, payload map[string]any) {
	if strings.TrimSpace(runID) == "" {
		return
	}
	if _, err := p.store.CreateRunEvent(domain.RunEvent{
		Type: eventType, RunID: runID, ConversationID: message.ConversationID, Payload: payload,
	}); err != nil {
		log.Printf("memory_provider_event_failed type=%s run_id=%s message_id=%s error=%q", eventType, runID, message.ID, err.Error())
	}
}

func candidatePayload(candidate domain.MemoryCandidate, syncKey string, status string, err error) map[string]any {
	errorMessage := ""
	if err != nil {
		errorMessage = err.Error()
	}
	payload, payloadErr := eventpkg.Payload(eventpkg.MemoryCandidatePayload{
		CandidateID: candidate.ID, SourceMessageID: candidate.SourceMessageID,
		SourceRole: candidate.SourceRole, Kind: candidate.Kind, Status: status,
		ExtractionReason: candidate.ExtractionReason, PolicyReason: candidate.PolicyReason,
		Confidence: candidate.Confidence, Error: errorMessage,
	})
	if payloadErr != nil {
		return failure.Merge(map[string]any{"status": status, "sync_key": syncKey, "error": payloadErr.Error()}, payloadErr)
	}
	payload["sync_key"] = syncKey
	return failure.Merge(payload, err)
}

func normalizeAdaptiveMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case AdaptiveModeAuto:
		return AdaptiveModeAuto
	case AdaptiveModeShadow:
		return AdaptiveModeShadow
	default:
		return AdaptiveModeOff
	}
}

func turnSyncKey(request TurnSyncRequest) string {
	return stableSyncKey(request.IdempotencyKey, request.RunID, request.Message)
}

func proposalKey(request ProposalRequest) string {
	return stableSyncKey(request.IdempotencyKey, request.RunID, request.Message)
}

func stableSyncKey(explicit string, runID string, message domain.Message) string {
	if value := strings.TrimSpace(explicit); value != "" {
		return value
	}
	if value := strings.TrimSpace(message.ID); value != "" {
		return "message:" + value
	}
	identity := strings.Join([]string{runID, message.ConversationID, message.Role, message.Content}, "\x00")
	hash := sha256.Sum256([]byte(identity))
	return "turn:" + hex.EncodeToString(hash[:12])
}

func candidateID(syncKey string) string {
	hash := sha256.Sum256([]byte(syncKey))
	return "memcand_" + hex.EncodeToString(hash[:12])
}

func clonePayload(payload map[string]any) map[string]any {
	cloned := make(map[string]any, len(payload)+1)
	for key, value := range payload {
		cloned[key] = value
	}
	return cloned
}
