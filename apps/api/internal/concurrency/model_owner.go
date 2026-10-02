package concurrency

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"agentflow-platform/apps/api/internal/inference/requestcontrol"
)

type ownerLimiter struct {
	active chan struct{}
	refs   int
}

// Close rejects new attempts and wakes all pending local waits. Transports are
// canceled by their own lifecycle; release remains idempotent after Close.
func (l *ModelRequestLimiter) Close() {
	if l != nil {
		l.closeCancel()
	}
}

func (l *ModelRequestLimiter) waitError(err error) error {
	if l.closed.Err() != nil {
		return &requestcontrol.OwnerAdmissionError{Code: "model_limiter_closed"}
	}
	return err
}

func (l *ModelRequestLimiter) acquireOwner(ctx context.Context) (func(), error) {
	limits := l.ownerLimits
	if limits.OwnerMaxConcurrent <= 0 {
		return func() {}, nil
	}
	timing := requestcontrol.AttemptTimingFromContext(ctx)
	if timing != nil {
		timing.OwnerLimited = true
	}
	owner := requestcontrol.OwnerFromContext(ctx)
	if limits.OwnerResolver != nil {
		var err error
		owner, err = limits.OwnerResolver(ctx)
		if err != nil {
			return nil, err
		}
	}
	owner = strings.TrimSpace(owner)
	if owner == "" {
		return nil, &requestcontrol.OwnerAdmissionError{Code: "model_owner_required"}
	}
	l.mu.Lock()
	entry := l.owners[owner]
	if entry == nil {
		entry = &ownerLimiter{active: make(chan struct{}, limits.OwnerMaxConcurrent)}
		l.owners[owner] = entry
	}
	if entry.refs >= limits.OwnerMaxConcurrent+max(0, limits.OwnerQueueSize) {
		l.mu.Unlock()
		return nil, &requestcontrol.OwnerAdmissionError{Code: "owner_model_queue_full"}
	}
	entry.refs++
	l.mu.Unlock()
	// ponytail: per-owner semaphore, not a fair scheduler. Owner slots are taken
	// before shared key/global capacity; strict fairness needs measured demand.
	waitCtx := ctx
	if limits.OwnerWaitTimeout > 0 {
		var cancel context.CancelFunc
		waitCtx, cancel = context.WithTimeout(ctx, limits.OwnerWaitTimeout)
		defer cancel()
	}
	started := time.Now()
	acquired := false
	select {
	case entry.active <- struct{}{}:
		acquired = true
	case <-waitCtx.Done():
	}
	if timing != nil {
		timing.OwnerWait = time.Since(started)
	}
	if err := waitCtx.Err(); err != nil {
		if acquired {
			<-entry.active
		}
		l.releaseOwnerRef(owner, entry)
		if ctx.Err() == nil && errors.Is(err, context.DeadlineExceeded) {
			return nil, &requestcontrol.OwnerAdmissionError{Code: "owner_model_queue_timeout"}
		}
		return nil, err
	}
	var once sync.Once
	return func() { once.Do(func() { <-entry.active; l.releaseOwnerRef(owner, entry) }) }, nil
}

func (l *ModelRequestLimiter) releaseOwnerRef(owner string, entry *ownerLimiter) {
	l.mu.Lock()
	defer l.mu.Unlock()
	entry.refs--
	if entry.refs == 0 {
		delete(l.owners, owner)
	}
}
