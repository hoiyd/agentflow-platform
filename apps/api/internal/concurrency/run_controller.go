package concurrency

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"agentflow-platform/apps/api/internal/failure"
)

var (
	ErrQueueFull    error = admissionError{code: "run_queue_full", message: "run queue is full"}
	ErrQueueTimeout error = admissionError{code: "run_queue_timeout", message: "run queue wait timed out"}
	ErrShuttingDown error = admissionError{code: "run_controller_shutting_down", message: "run controller is shutting down"}
)

type admissionError struct {
	code    string
	message string
}

func (e admissionError) Error() string { return e.message }
func (e admissionError) FailureInfo() failure.Info {
	return failure.Info{Code: e.code, Source: "run_admission", Category: failure.CategoryCapacity, Retryable: true}
}

const (
	DefaultMaxConcurrentRuns = 8
	DefaultRunQueueSize      = 32
	DefaultRunQueueWait      = 30 * time.Second
)

type RunOptions struct {
	MaxConcurrent int
	QueueSize     int
	WaitTimeout   time.Duration
}

type RunController struct {
	active      chan struct{}
	capacity    chan struct{}
	waitTimeout time.Duration
	writers     conversationWriters
	mu          sync.Mutex
	closing     bool
	pending     sync.WaitGroup
}

type Reservation struct {
	controller *RunController
	once       sync.Once
}

func NewRunController(options RunOptions) *RunController {
	if options.MaxConcurrent <= 0 {
		options.MaxConcurrent = 1
	}
	if options.QueueSize < 0 {
		options.QueueSize = 0
	}
	if options.WaitTimeout <= 0 {
		options.WaitTimeout = 30 * time.Second
	}
	return &RunController{
		active:      make(chan struct{}, options.MaxConcurrent),
		capacity:    make(chan struct{}, options.MaxConcurrent+options.QueueSize),
		waitTimeout: options.WaitTimeout,
		writers:     conversationWriters{entries: make(map[string]*writerEntry)},
	}
}

// Reserve rejects excess work before it can create or mutate conversation state.
func (c *RunController) Reserve() (*Reservation, error) {
	if c == nil {
		return &Reservation{}, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closing {
		return nil, ErrShuttingDown
	}
	select {
	case c.capacity <- struct{}{}:
		c.pending.Add(1)
		return &Reservation{controller: c}, nil
	default:
		return nil, ErrQueueFull
	}
}

// Start waits for exclusive conversation ownership and an active run slot.
func (r *Reservation) Start(ctx context.Context, conversationID string) (func(), error) {
	if r == nil || r.controller == nil {
		return func() {}, nil
	}
	if conversationID == "" {
		r.Cancel()
		return nil, errors.New("conversation id is required")
	}

	waitCtx, cancel := context.WithTimeout(ctx, r.controller.waitTimeout)
	defer cancel()

	releaseWriter, err := r.controller.writers.acquire(waitCtx, conversationID)
	if err != nil {
		r.Cancel()
		return nil, queueWaitError(ctx, err)
	}
	select {
	case r.controller.active <- struct{}{}:
	case <-waitCtx.Done():
		releaseWriter()
		r.Cancel()
		return nil, queueWaitError(ctx, waitCtx.Err())
	}

	var releaseOnce sync.Once
	return func() {
		releaseOnce.Do(func() {
			<-r.controller.active
			releaseWriter()
			r.Cancel()
		})
	}, nil
}

func (r *Reservation) Cancel() {
	if r == nil || r.controller == nil {
		return
	}
	r.once.Do(func() {
		<-r.controller.capacity
		r.controller.pending.Done()
	})
}

// CloseAndWait stops admission and waits until all accepted reservations have
// either completed or been canceled. It is safe to call more than once.
func (c *RunController) CloseAndWait(ctx context.Context) error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	c.closing = true
	c.mu.Unlock()
	done := make(chan struct{})
	go func() {
		c.pending.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func queueWaitError(parent context.Context, err error) error {
	if parent.Err() != nil {
		return parent.Err()
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return ErrQueueTimeout
	}
	return err
}

type conversationWriters struct {
	mu      sync.Mutex
	entries map[string]*writerEntry
}

type writerEntry struct {
	token chan struct{}
	refs  int
}

func (w *conversationWriters) acquire(ctx context.Context, conversationID string) (func(), error) {
	w.mu.Lock()
	entry := w.entries[conversationID]
	if entry == nil {
		entry = &writerEntry{token: make(chan struct{}, 1)}
		w.entries[conversationID] = entry
	}
	entry.refs++
	w.mu.Unlock()

	select {
	case entry.token <- struct{}{}:
		var once sync.Once
		return func() {
			once.Do(func() {
				<-entry.token
				w.releaseRef(conversationID, entry)
			})
		}, nil
	case <-ctx.Done():
		w.releaseRef(conversationID, entry)
		return nil, ctx.Err()
	}
}

func (w *conversationWriters) releaseRef(conversationID string, entry *writerEntry) {
	w.mu.Lock()
	defer w.mu.Unlock()
	entry.refs--
	if entry.refs == 0 {
		delete(w.entries, conversationID)
	}
	if entry.refs < 0 {
		panic(fmt.Sprintf("negative writer references for conversation %q", conversationID))
	}
}
