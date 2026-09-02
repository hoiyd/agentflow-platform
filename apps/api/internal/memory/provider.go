package memory

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/failure"
	"agentflow-platform/apps/api/internal/modelprovider"
)

const (
	defaultSyncQueueSize  = 256
	defaultSyncJobTimeout = 30 * time.Second
	defaultMaxAttempts    = 3
	defaultRetryBaseDelay = 100 * time.Millisecond
)

var (
	ErrProviderNotInitialized = failure.New(failure.Definition{
		Message: "memory provider is not initialized",
		Info: failure.Info{
			Code: "memory_provider_not_initialized", Source: "memory_provider",
			Category: failure.CategoryAvailability, Retryable: false,
		},
	})
	ErrProviderClosed = failure.New(failure.Definition{
		Message: "memory provider is closed",
		Info: failure.Info{
			Code: "memory_provider_closed", Source: "memory_provider",
			Category: failure.CategoryAvailability, Retryable: false,
		},
	})
	ErrSyncQueueFull = failure.New(failure.Definition{
		Message: "memory turn sync queue is full",
		Info: failure.Info{
			Code: "memory_sync_queue_full", Source: "memory_provider",
			Category: failure.CategoryCapacity, Retryable: true,
		},
	})
)

type Embedder interface {
	EmbedText(context.Context, string) (modelprovider.Embedding, error)
}

type ProviderStore interface {
	CreateMemoryCandidate(domain.MemoryCandidate) (domain.MemoryCandidate, bool, error)
	CreateMemory(domain.Memory, domain.MemoryEmbedding) (domain.Memory, error)
	SearchMemories(domain.MemorySearch) ([]domain.RetrievedMemory, error)
	CreateRunEvent(domain.RunEvent) (domain.RunEvent, error)
}

// Recaller is the only Memory capability needed by Agent Runtime.
type Recaller interface {
	Recall(context.Context, domain.MemorySearch) ([]domain.RetrievedMemory, error)
}

// Operations is the transport-facing explicit Memory surface.
type Operations interface {
	Recall(context.Context, domain.MemorySearch) ([]domain.RetrievedMemory, error)
	Commit(context.Context, domain.Memory) (domain.Memory, error)
}

// TurnSyncer accepts auxiliary post-response work without blocking the Run.
type TurnSyncer interface {
	SyncTurn(TurnSyncRequest) error
}

type ProviderOptions struct {
	QueueSize      int
	JobTimeout     time.Duration
	MaxAttempts    int
	RetryBaseDelay time.Duration
	Extractor      CandidateExtractor
	Policy         CandidatePolicy
	AdaptiveMode   string
}

type providerState uint8

const (
	providerCreated providerState = iota
	providerRunning
	providerClosing
	providerClosed
)

// BuiltinProvider adapts the existing File/Postgres persistence and embedding
// client to the provider lifecycle without exposing either to Runtime or HTTP.
type BuiltinProvider struct {
	store        ProviderStore
	embedder     Embedder
	extractor    CandidateExtractor
	policy       CandidatePolicy
	adaptiveMode string
	options      ProviderOptions

	ctx    context.Context
	cancel context.CancelFunc
	jobs   chan TurnSyncRequest
	done   chan struct{}

	mu       sync.Mutex
	state    providerState
	inflight map[string]struct{}
}

var (
	_ Operations = (*BuiltinProvider)(nil)
	_ Recaller   = (*BuiltinProvider)(nil)
	_ TurnSyncer = (*BuiltinProvider)(nil)
)

func NewBuiltinProvider(store ProviderStore, embedder Embedder, options ProviderOptions) *BuiltinProvider {
	if options.QueueSize <= 0 {
		options.QueueSize = defaultSyncQueueSize
	}
	if options.JobTimeout <= 0 {
		options.JobTimeout = defaultSyncJobTimeout
	}
	if options.MaxAttempts <= 0 {
		options.MaxAttempts = defaultMaxAttempts
	}
	if options.RetryBaseDelay <= 0 {
		options.RetryBaseDelay = defaultRetryBaseDelay
	}
	if options.Extractor == nil {
		options.Extractor = RuleBasedCandidateExtractor{}
	}
	if options.Policy == nil {
		options.Policy = ConservativeCandidatePolicy{}
	}
	options.AdaptiveMode = normalizeAdaptiveMode(options.AdaptiveMode)
	ctx, cancel := context.WithCancel(context.Background())
	return &BuiltinProvider{
		store: store, embedder: embedder, extractor: options.Extractor, policy: options.Policy,
		adaptiveMode: options.AdaptiveMode, options: options,
		ctx: ctx, cancel: cancel, jobs: make(chan TurnSyncRequest, options.QueueSize), done: make(chan struct{}),
		state: providerCreated, inflight: map[string]struct{}{},
	}
}

func (p *BuiltinProvider) Initialize(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return newOperationError("initialize", 1, err)
	}
	if p == nil || p.store == nil || p.embedder == nil {
		return newOperationError("initialize", 1, errors.New("memory provider store and embedder are required"))
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	switch p.state {
	case providerCreated:
		p.state = providerRunning
		go p.runSyncWorker()
		return nil
	case providerRunning:
		return nil
	default:
		return ErrProviderClosed
	}
}

func (p *BuiltinProvider) Recall(ctx context.Context, search domain.MemorySearch) ([]domain.RetrievedMemory, error) {
	if err := p.requireRunning(false); err != nil {
		return nil, err
	}
	search.Query = strings.TrimSpace(search.Query)
	if search.Query == "" {
		return nil, newOperationError("recall", 1, errors.New("query is required"))
	}
	if len(search.Embedding) == 0 {
		var embedding modelprovider.Embedding
		if err := p.retry(ctx, "recall.embed", func() error {
			var err error
			embedding, err = p.embedder.EmbedText(ctx, search.Query)
			return err
		}); err != nil {
			return nil, EmbeddingError{Err: err}
		}
		search.Embedding = embedding.Vector
		search.EmbeddingProvider = embedding.Provider
		search.EmbeddingModel = embedding.Model
	}
	var items []domain.RetrievedMemory
	if err := p.retry(ctx, "recall.search", func() error {
		var err error
		items, err = p.store.SearchMemories(search)
		return err
	}); err != nil {
		return nil, err
	}
	return items, nil
}

func (p *BuiltinProvider) Commit(ctx context.Context, item domain.Memory) (domain.Memory, error) {
	return p.commit(ctx, item, false)
}

func (p *BuiltinProvider) commit(ctx context.Context, item domain.Memory, allowClosing bool) (domain.Memory, error) {
	if err := p.requireRunning(allowClosing); err != nil {
		return domain.Memory{}, err
	}
	item.Kind = strings.TrimSpace(item.Kind)
	item.Content = strings.TrimSpace(item.Content)
	if item.Kind == "" {
		return domain.Memory{}, newOperationError("commit", 1, errors.New("memory kind is required"))
	}
	if item.Content == "" {
		return domain.Memory{}, newOperationError("commit", 1, errors.New("memory content is required"))
	}
	if strings.TrimSpace(item.ID) == "" {
		id, err := newMemoryID()
		if err != nil {
			return domain.Memory{}, newOperationError("commit", 1, err)
		}
		item.ID = id
	}
	var embedding modelprovider.Embedding
	if err := p.retry(ctx, "commit.embed", func() error {
		var err error
		embedding, err = p.embedder.EmbedText(ctx, item.Content)
		return err
	}); err != nil {
		return domain.Memory{}, EmbeddingError{Err: err}
	}
	var created domain.Memory
	if err := p.retry(ctx, "commit.store", func() error {
		var err error
		created, err = p.store.CreateMemory(item, domain.MemoryEmbedding{
			Provider: embedding.Provider, Model: embedding.Model,
			Dimensions: len(embedding.Vector), Embedding: embedding.Vector,
		})
		return err
	}); err != nil {
		return domain.Memory{}, err
	}
	return created, nil
}

func (p *BuiltinProvider) Close(ctx context.Context) error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	switch p.state {
	case providerCreated:
		p.state = providerClosed
		p.cancel()
		close(p.done)
	case providerRunning:
		p.state = providerClosing
		close(p.jobs)
	case providerClosing:
	case providerClosed:
		p.mu.Unlock()
		return nil
	}
	done := p.done
	p.mu.Unlock()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		p.cancel()
		return newOperationError("close", 1, ctx.Err())
	}
}

func (p *BuiltinProvider) requireRunning(allowClosing bool) error {
	if p == nil {
		return ErrProviderNotInitialized
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.state == providerRunning || (allowClosing && p.state == providerClosing) {
		return nil
	}
	if p.state == providerCreated {
		return ErrProviderNotInitialized
	}
	return ErrProviderClosed
}

func (p *BuiltinProvider) retry(ctx context.Context, operation string, call func() error) error {
	maxAttempts := p.options.MaxAttempts
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		err := call()
		if err == nil {
			return nil
		}
		if attempt == maxAttempts || !providerRetryable(err) {
			return newOperationError(operation, attempt, err)
		}
		delay := p.options.RetryBaseDelay * time.Duration(1<<(attempt-1))
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return newOperationError(operation, attempt, ctx.Err())
		case <-timer.C:
		}
	}
	return nil
}

func providerRetryable(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	info := failure.Describe(err)
	if info.Retryable {
		return true
	}
	switch info.Category {
	case failure.CategoryAvailability, failure.CategoryTimeout, failure.CategoryInternal:
		return true
	default:
		return info.Code == failure.CodeUnclassified
	}
}

type operationError struct {
	operation string
	attempts  int
	err       error
}

func newOperationError(operation string, attempts int, err error) error {
	if err == nil {
		return nil
	}
	return &operationError{operation: operation, attempts: attempts, err: err}
}

func (e *operationError) Error() string { return e.err.Error() }
func (e *operationError) Unwrap() error { return e.err }
func (e *operationError) FailureInfo() failure.Info {
	info := failure.Describe(e.err)
	if info.Code == failure.CodeUnclassified {
		info.Code = "memory_provider_operation_failed"
	}
	info.Source = "memory_provider"
	info.Operation = e.operation
	info.Details = cloneDetails(info.Details)
	info.Details["attempts"] = e.attempts
	return info
}

// EmbeddingError preserves the HTTP distinction between invalid input and an
// unavailable embedding dependency while retaining provider operation details.
type EmbeddingError struct{ Err error }

func (e EmbeddingError) Error() string { return e.Err.Error() }
func (e EmbeddingError) Unwrap() error { return e.Err }
func (e EmbeddingError) FailureInfo() failure.Info {
	info := failure.Describe(e.Err)
	if info.Source == "application" {
		info.Source = "memory_provider"
	}
	return info
}

func IsEmbeddingError(err error) bool {
	var target EmbeddingError
	return errors.As(err, &target)
}

func cloneDetails(value map[string]any) map[string]any {
	result := make(map[string]any, len(value)+1)
	for key, item := range value {
		result[key] = item
	}
	return result
}

func newMemoryID() (string, error) {
	value := make([]byte, 12)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return "mem_" + hex.EncodeToString(value), nil
}
