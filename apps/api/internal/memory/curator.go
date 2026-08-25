package memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log"
	"strings"
	"sync"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	eventpkg "agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/failure"
	"agentflow-platform/apps/api/internal/modelprovider"
)

const (
	defaultCurationQueueSize  = 256
	defaultCurationJobTimeout = 30 * time.Second
	AdaptiveModeOff           = "off"
	AdaptiveModeShadow        = "shadow"
	AdaptiveModeAuto          = "auto"
)

var (
	ErrCurationQueueFull = failure.New(failure.Definition{
		Message: "memory curation queue is full",
		Info: failure.Info{
			Code: "memory_curation_queue_full", Source: "memory_curator",
			Category: failure.CategoryCapacity, Retryable: true,
		},
	})
	ErrCuratorClosed = failure.New(failure.Definition{
		Message: "memory curator is closed",
		Info: failure.Info{
			Code: "memory_curator_closed", Source: "memory_curator",
			Category: failure.CategoryAvailability, Retryable: false,
		},
	})
)

type Embedder interface {
	EmbedText(context.Context, string) (modelprovider.Embedding, error)
}

type CuratorStore interface {
	CreateMemoryCandidate(domain.MemoryCandidate) (domain.MemoryCandidate, bool, error)
	CreateMemory(domain.Memory, domain.MemoryEmbedding) (domain.Memory, error)
	CreateRunEvent(domain.RunEvent) (domain.RunEvent, error)
}

type CurationJob struct {
	RunID   string
	Message domain.Message
}

type CuratorOptions struct {
	QueueSize    int
	JobTimeout   time.Duration
	Extractor    CandidateExtractor
	Policy       CandidatePolicy
	AdaptiveMode string
}

// Curator turns durable user statements into auditable candidates and
// long-term memory without delaying the primary response.
type Curator struct {
	store        CuratorStore
	embedder     Embedder
	extractor    CandidateExtractor
	policy       CandidatePolicy
	adaptiveMode string
	jobs         chan CurationJob
	jobTimeout   time.Duration
	ctx          context.Context
	cancel       context.CancelFunc

	mu     sync.Mutex
	closed bool
	wg     sync.WaitGroup
}

func NewCurator(store CuratorStore, embedder Embedder) *Curator {
	return NewCuratorWithOptions(store, embedder, CuratorOptions{})
}

func NewCuratorWithOptions(store CuratorStore, embedder Embedder, options CuratorOptions) *Curator {
	if options.QueueSize <= 0 {
		options.QueueSize = defaultCurationQueueSize
	}
	if options.JobTimeout <= 0 {
		options.JobTimeout = defaultCurationJobTimeout
	}
	if options.Extractor == nil {
		options.Extractor = RuleBasedCandidateExtractor{}
	}
	if options.Policy == nil {
		options.Policy = ConservativeCandidatePolicy{}
	}
	options.AdaptiveMode = normalizeAdaptiveMode(options.AdaptiveMode)
	ctx, cancel := context.WithCancel(context.Background())
	curator := &Curator{
		store: store, embedder: embedder, extractor: options.Extractor, policy: options.Policy,
		adaptiveMode: options.AdaptiveMode,
		jobs:         make(chan CurationJob, options.QueueSize), jobTimeout: options.JobTimeout,
		ctx: ctx, cancel: cancel,
	}
	curator.wg.Add(1)
	go curator.run()
	return curator
}

// Enqueue never waits for extraction, embedding, persistence, or queue capacity.
func (c *Curator) Enqueue(job CurationJob) error {
	if strings.TrimSpace(job.Message.Content) == "" || !strings.EqualFold(strings.TrimSpace(job.Message.Role), "user") {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return ErrCuratorClosed
	}
	select {
	case c.jobs <- job:
		return nil
	default:
		return ErrCurationQueueFull
	}
}

// Close stops accepting work and waits for already queued jobs to finish.
func (c *Curator) Close(ctx context.Context) error {
	c.mu.Lock()
	if !c.closed {
		c.closed = true
		close(c.jobs)
	}
	c.mu.Unlock()

	done := make(chan struct{})
	go func() {
		c.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		c.cancel()
		return nil
	case <-ctx.Done():
		c.cancel()
		return ctx.Err()
	}
}

func (c *Curator) run() {
	defer c.wg.Done()
	for job := range c.jobs {
		c.curate(job)
	}
}

func (c *Curator) curate(job CurationJob) {
	ctx, cancel := context.WithTimeout(c.ctx, c.jobTimeout)
	draft, ok, err := c.extractor.Extract(ctx, job.Message)
	cancel()
	if err != nil {
		candidate := domain.MemoryCandidate{
			ID: candidateID(job), ConversationID: job.Message.ConversationID, RunID: strings.TrimSpace(job.RunID),
			SourceMessageID: job.Message.ID, SourceRole: strings.TrimSpace(job.Message.Role),
		}
		c.publish(job, domain.EventMemoryCandidateFailed, candidatePayload(candidate, "failed", err))
		log.Printf("memory_candidate_extraction_failed run_id=%s message_id=%s error=%q", job.RunID, job.Message.ID, err.Error())
		return
	}
	if !ok {
		return
	}
	decision := c.policy.Evaluate(job.Message, draft)
	if decision.Accepted && draft.ExtractionReason == CandidateReasonAdaptive && c.adaptiveMode != AdaptiveModeAuto {
		decision = PolicyDecision{Reason: PolicyRejectShadowMode}
	}
	status := domain.MemoryCandidateRejected
	if decision.Accepted {
		status = domain.MemoryCandidateAccepted
	}
	candidateContent := draft.Content
	if decision.Reason == PolicyRejectSecret {
		candidateContent = "[redacted potential secret]"
	}
	candidate := domain.MemoryCandidate{
		ID: candidateID(job), ConversationID: job.Message.ConversationID, RunID: strings.TrimSpace(job.RunID),
		SourceMessageID: job.Message.ID, SourceRole: strings.TrimSpace(job.Message.Role),
		Kind: draft.Kind, Content: candidateContent, Status: status,
		ExtractionReason: draft.ExtractionReason, PolicyReason: decision.Reason,
		Confidence: draft.Confidence,
		CreatedAt:  time.Now().UTC(),
	}
	createdCandidate, created, err := c.store.CreateMemoryCandidate(candidate)
	if err != nil {
		c.publish(job, domain.EventMemoryCandidateFailed, candidatePayload(candidate, "failed", err))
		log.Printf("memory_candidate_persist_failed run_id=%s message_id=%s error=%q", job.RunID, job.Message.ID, err.Error())
		return
	}
	if !created {
		return
	}
	candidate = createdCandidate
	c.publish(job, domain.EventMemoryCandidateProposed, candidatePayload(candidate, "proposed", nil))
	if !decision.Accepted {
		c.publish(job, domain.EventMemoryCandidateRejected, candidatePayload(candidate, "rejected", nil))
		return
	}
	c.publish(job, domain.EventMemoryCandidateAccepted, candidatePayload(candidate, "accepted", nil))
	c.commitCandidate(job, candidate)
}

func (c *Curator) commitCandidate(job CurationJob, candidate domain.MemoryCandidate) {
	ctx, cancel := context.WithTimeout(c.ctx, c.jobTimeout)
	defer cancel()
	startedAt := time.Now()
	payload := map[string]any{
		"candidate_id": candidate.ID, "message_id": job.Message.ID,
		"role": job.Message.Role, "kind": candidate.Kind, "source": "memory_curator",
	}
	c.publish(job, domain.EventMemorySyncRequested, payload)

	embedding, err := c.embedder.EmbedText(ctx, candidate.Content)
	if err == nil {
		_, err = c.store.CreateMemory(domain.Memory{
			ID:              "mem_curated_" + candidate.ID,
			WorkspaceID:     job.Message.WorkspaceID,
			ConversationID:  candidate.ConversationID,
			RunID:           candidate.RunID,
			SourceMessageID: candidate.SourceMessageID,
			Kind:            candidate.Kind,
			Content:         candidate.Content,
			Metadata: map[string]any{
				"candidate_id": candidate.ID, "source_role": candidate.SourceRole,
				"extraction_reason": candidate.ExtractionReason,
			},
			CreatedAt: candidate.CreatedAt,
		}, domain.MemoryEmbedding{
			Provider: embedding.Provider, Model: embedding.Model,
			Dimensions: len(embedding.Vector), Embedding: embedding.Vector,
		})
	}
	if err != nil {
		failed := clonePayload(payload)
		failed["error"] = err.Error()
		failed["duration_ms"] = time.Since(startedAt).Milliseconds()
		failed = failure.Merge(failed, err)
		c.publish(job, domain.EventMemorySyncFailed, failed)
		log.Printf("memory_curation_failed run_id=%s candidate_id=%s error=%q", job.RunID, candidate.ID, err.Error())
		return
	}

	completed := clonePayload(payload)
	completed["memory_id"] = "mem_curated_" + candidate.ID
	completed["embedding_provider"] = embedding.Provider
	completed["embedding_model"] = embedding.Model
	completed["duration_ms"] = time.Since(startedAt).Milliseconds()
	c.publish(job, domain.EventMemorySyncCompleted, completed)
}

func (c *Curator) publish(job CurationJob, eventType domain.RunEventType, payload map[string]any) {
	if strings.TrimSpace(job.RunID) == "" {
		return
	}
	if _, err := c.store.CreateRunEvent(domain.RunEvent{
		Type: eventType, RunID: job.RunID, ConversationID: job.Message.ConversationID, Payload: payload,
	}); err != nil {
		log.Printf("memory_curation_event_failed type=%s run_id=%s message_id=%s error=%q", eventType, job.RunID, job.Message.ID, err.Error())
	}
}

func candidatePayload(candidate domain.MemoryCandidate, status string, err error) map[string]any {
	errorMessage := ""
	if err != nil {
		errorMessage = err.Error()
	}
	payload, payloadErr := eventpkg.Payload(eventpkg.MemoryCandidatePayload{
		CandidateID: candidate.ID, SourceMessageID: candidate.SourceMessageID,
		SourceRole: candidate.SourceRole, Kind: candidate.Kind, Status: status,
		ExtractionReason: candidate.ExtractionReason, PolicyReason: candidate.PolicyReason,
		Confidence: candidate.Confidence,
		Error:      errorMessage,
	})
	if payloadErr != nil {
		return failure.Merge(map[string]any{"status": status, "error": payloadErr.Error()}, payloadErr)
	}
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

func candidateID(job CurationJob) string {
	identity := strings.TrimSpace(job.Message.ID)
	if identity == "" {
		identity = strings.Join([]string{job.RunID, job.Message.ConversationID, job.Message.Role, job.Message.Content}, "\x00")
	}
	hash := sha256.Sum256([]byte(identity))
	return "memcand_" + hex.EncodeToString(hash[:12])
}

func clonePayload(payload map[string]any) map[string]any {
	cloned := make(map[string]any, len(payload)+1)
	for key, value := range payload {
		cloned[key] = value
	}
	return cloned
}
