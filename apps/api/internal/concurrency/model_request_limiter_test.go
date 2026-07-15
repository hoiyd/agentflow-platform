package concurrency

import (
	"context"
	"errors"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/modelrequest"
)

func TestModelRequestLimiterEnforcesGlobalSemaphore(t *testing.T) {
	limiter := NewModelRequestLimiter(ModelRequestLimits{MaxConcurrent: 1})
	releaseFirst, err := limiter.AcquireRequest(context.Background(), "", 1)
	if err != nil {
		t.Fatalf("acquire first: %v", err)
	}

	acquired := make(chan func(), 1)
	go func() {
		release, acquireErr := limiter.AcquireRequest(context.Background(), "", 1)
		if acquireErr == nil {
			acquired <- release
		}
	}()
	select {
	case release := <-acquired:
		release()
		t.Fatal("second request bypassed global semaphore")
	case <-time.After(30 * time.Millisecond):
	}

	releaseFirst()
	select {
	case release := <-acquired:
		release()
	case <-time.After(time.Second):
		t.Fatal("second request did not acquire released semaphore")
	}
}

func TestModelRequestLimiterUsesAPIKeyTokenBuckets(t *testing.T) {
	limiter := NewModelRequestLimiter(ModelRequestLimits{
		MaxConcurrent:     2,
		RequestsPerPeriod: 1, TokensPerPeriod: 10, RatePeriod: 50 * time.Millisecond,
	})
	release, err := limiter.AcquireRequest(context.Background(), "secret-key", 1)
	if err != nil {
		t.Fatalf("acquire first: %v", err)
	}
	release()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := limiter.AcquireRequest(ctx, "secret-key", 1); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected rate-limit wait to honor context deadline, got %v", err)
	}
}

func TestModelRequestLimiterRejectsRequestAboveTokenCapacity(t *testing.T) {
	limiter := NewModelRequestLimiter(ModelRequestLimits{
		MaxConcurrent:   1,
		TokensPerPeriod: 10, RatePeriod: time.Minute,
	})
	if _, err := limiter.AcquireRequest(context.Background(), "secret-key", 11); err == nil {
		t.Fatal("expected token capacity error")
	} else {
		var limitErr *modelrequest.TokenLimitError
		if !errors.As(err, &limitErr) || limitErr.EstimatedTokens != 11 || limitErr.Capacity != 10 {
			t.Fatalf("unexpected token capacity error: %#v", err)
		}
	}
}
