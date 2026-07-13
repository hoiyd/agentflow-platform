package memory

import (
	"context"
	"errors"
	"log"
	"strings"
	"sync"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/openai"
)

const (
	defaultQueueSize  = 256
	defaultJobTimeout = 30 * time.Second
)

var (
	ErrQueueFull = errors.New("memory sync queue is full")
	ErrClosed    = errors.New("memory syncer is closed")
)

type Embedder interface {
	EmbedText(context.Context, string) (openai.Embedding, error)
}

type Store interface {
	CreateMemory(domain.Memory, domain.MemoryEmbedding) (domain.Memory, error)
	CreateRunEvent(domain.RunEvent) (domain.RunEvent, error)
}

type Job struct {
	RunID   string
	Message domain.Message
}

type Syncer struct {
	store      Store
	embedder   Embedder
	jobs       chan Job
	jobTimeout time.Duration
	ctx        context.Context
	cancel     context.CancelFunc

	mu     sync.Mutex
	closed bool
	wg     sync.WaitGroup
}

func NewSyncer(store Store, embedder Embedder) *Syncer {
	return NewSyncerWithOptions(store, embedder, defaultQueueSize, defaultJobTimeout)
}

func NewSyncerWithOptions(store Store, embedder Embedder, queueSize int, jobTimeout time.Duration) *Syncer {
	if queueSize <= 0 {
		queueSize = defaultQueueSize
	}
	if jobTimeout <= 0 {
		jobTimeout = defaultJobTimeout
	}
	ctx, cancel := context.WithCancel(context.Background())
	syncer := &Syncer{
		store: store, embedder: embedder, jobs: make(chan Job, queueSize), jobTimeout: jobTimeout,
		ctx: ctx, cancel: cancel,
	}
	syncer.wg.Add(1)
	go syncer.run()
	return syncer
}

// Enqueue never waits for embedding, persistence, or queue capacity.
func (s *Syncer) Enqueue(job Job) error {
	if strings.TrimSpace(job.Message.Content) == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	select {
	case s.jobs <- job:
		return nil
	default:
		return ErrQueueFull
	}
}

// Close stops accepting work and waits for already queued jobs to finish.
func (s *Syncer) Close(ctx context.Context) error {
	s.mu.Lock()
	if !s.closed {
		s.closed = true
		close(s.jobs)
	}
	s.mu.Unlock()

	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		s.cancel()
		return nil
	case <-ctx.Done():
		s.cancel()
		return ctx.Err()
	}
}

func (s *Syncer) run() {
	defer s.wg.Done()
	for job := range s.jobs {
		s.sync(job)
	}
}

func (s *Syncer) sync(job Job) {
	ctx, cancel := context.WithTimeout(s.ctx, s.jobTimeout)
	defer cancel()
	startedAt := time.Now()

	payload := map[string]any{
		"message_id": job.Message.ID,
		"role":       job.Message.Role,
		"kind":       "message",
		"source":     "memory_sync",
	}
	s.publish(job, domain.EventMemorySyncRequested, payload)

	embedding, err := s.embedder.EmbedText(ctx, strings.TrimSpace(job.Message.Content))
	if err == nil {
		_, err = s.store.CreateMemory(domain.Memory{
			ID:              "mem_msg_" + job.Message.ID,
			ConversationID:  job.Message.ConversationID,
			RunID:           strings.TrimSpace(job.RunID),
			SourceMessageID: job.Message.ID,
			Kind:            "message",
			Content:         strings.TrimSpace(job.Message.Content),
			Metadata:        map[string]any{"role": job.Message.Role},
			CreatedAt:       job.Message.CreatedAt,
		}, domain.MemoryEmbedding{
			Provider: embedding.Provider, Model: embedding.Model,
			Dimensions: len(embedding.Vector), Embedding: embedding.Vector,
		})
	}
	if err != nil {
		failed := clonePayload(payload)
		failed["error"] = err.Error()
		failed["duration_ms"] = time.Since(startedAt).Milliseconds()
		s.publish(job, domain.EventMemorySyncFailed, failed)
		log.Printf("memory_sync_failed run_id=%s message_id=%s error=%q", job.RunID, job.Message.ID, err.Error())
		return
	}

	completed := clonePayload(payload)
	completed["memory_id"] = "mem_msg_" + job.Message.ID
	completed["embedding_provider"] = embedding.Provider
	completed["embedding_model"] = embedding.Model
	completed["duration_ms"] = time.Since(startedAt).Milliseconds()
	s.publish(job, domain.EventMemorySyncCompleted, completed)
}

func (s *Syncer) publish(job Job, eventType domain.RunEventType, payload map[string]any) {
	if strings.TrimSpace(job.RunID) == "" {
		return
	}
	if _, err := s.store.CreateRunEvent(domain.RunEvent{
		Type: eventType, RunID: job.RunID, ConversationID: job.Message.ConversationID, Payload: payload,
	}); err != nil {
		log.Printf("memory_sync_event_failed type=%s run_id=%s message_id=%s error=%q", eventType, job.RunID, job.Message.ID, err.Error())
	}
}

func clonePayload(payload map[string]any) map[string]any {
	cloned := make(map[string]any, len(payload)+1)
	for key, value := range payload {
		cloned[key] = value
	}
	return cloned
}
