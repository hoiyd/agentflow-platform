package tool

import (
	"context"
	"sync"
	"time"
	"unicode/utf8"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/redaction"
)

const (
	MaxProgressMessageBytes = domain.MaxToolProgressMessageBytes
	MaxProgressMessages     = 32
	MaxProgressBytes        = 16 * 1024
	progressInterval        = 250 * time.Millisecond
)

// ExecutionProgressTracer observes sanitized display updates independently of
// ToolProgressGuard (which detects repeated calls/results). No progress is ever
// sent as a Tool observation or used to satisfy a completion gate.
type ExecutionProgressTracer interface {
	ToolProgressUpdated(context.Context, ExecutionRequest, domain.ToolProgressUpdate)
}

type progressContextKey struct{}

type executionProgress struct {
	mu                        sync.Mutex
	ctx                       context.Context
	wake                      chan struct{}
	pending                   *domain.ToolProgressUpdate
	closed                    bool
	attempts, messages, bytes int
}

func newExecutionProgress(ctx context.Context) (context.Context, *executionProgress) {
	sink := &executionProgress{ctx: ctx, wake: make(chan struct{}, 1)}
	return context.WithValue(ctx, progressContextKey{}, sink), sink
}

// ReportProgress is optional and nonblocking with respect to consumers. true
// means accepted for coalescing, not persisted or delivered. The Executor owns
// identity, bounds and revocation; Bindings must not supply runtime IDs.
// Report complete semantic messages, never raw stdout or partial Secret tokens.
func ReportProgress(ctx context.Context, update domain.ToolProgressUpdate) bool {
	sink, _ := ctx.Value(progressContextKey{}).(*executionProgress)
	if sink == nil {
		return false
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if sink.closed || sink.ctx.Err() != nil || sink.attempts >= 1024 || sink.messages >= MaxProgressMessages || sink.bytes >= MaxProgressBytes {
		return false
	}
	sink.attempts++
	// Reject unbounded inputs before regex redaction. A larger semantic message
	// may be truncated, but an arbitrarily large producer payload is refused.
	if len(update.Message) > 8192 || !utf8.ValidString(update.Message) {
		return false
	}
	update.Message, _ = redaction.Text(update.Message)
	if len(update.Message) > MaxProgressMessageBytes {
		update.Message = truncateUTF8([]byte(update.Message), MaxProgressMessageBytes)
		update.Truncated = true
	}
	if update.Validate() != nil {
		return false
	}
	if update.Completed != nil {
		completed, total := *update.Completed, *update.Total
		update.Completed, update.Total = &completed, &total
	}
	sink.pending = &update
	select {
	case sink.wake <- struct{}{}:
	default:
	}
	return true
}

// publish drains at most one replacement. No background pump or unbounded
// queue: the Executor consumes updates while awaiting the original Handler.
func (s *executionProgress) publish(emit func(domain.ToolProgressUpdate)) bool {
	s.mu.Lock()
	if s.closed || s.ctx.Err() != nil || s.pending == nil || s.messages >= MaxProgressMessages || s.bytes >= MaxProgressBytes {
		s.mu.Unlock()
		return false
	}
	update := *s.pending
	s.pending = nil
	remaining := MaxProgressBytes - s.bytes
	if len(update.Message) > remaining {
		update.Message = truncateUTF8([]byte(update.Message), remaining)
		update.Truncated = true
	}
	s.messages++
	s.bytes += len(update.Message)
	s.mu.Unlock()
	emit(update)
	return true
}

func (s *executionProgress) close() {
	s.mu.Lock()
	s.closed, s.pending = true, nil
	s.mu.Unlock()
}
