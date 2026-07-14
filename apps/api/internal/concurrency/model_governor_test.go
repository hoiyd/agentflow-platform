package concurrency

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestModelGovernorEnforcesGlobalSemaphore(t *testing.T) {
	governor := NewModelGovernor(ModelOptions{MaxConcurrent: 1})
	releaseFirst, err := governor.Acquire(context.Background(), "", 1)
	if err != nil {
		t.Fatalf("acquire first: %v", err)
	}

	acquired := make(chan func(), 1)
	go func() {
		release, acquireErr := governor.Acquire(context.Background(), "", 1)
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

func TestModelGovernorUsesAPIKeyTokenBuckets(t *testing.T) {
	governor := NewModelGovernor(ModelOptions{
		MaxConcurrent:     2,
		RequestsPerPeriod: 1, TokensPerPeriod: 10, RatePeriod: 50 * time.Millisecond,
	})
	release, err := governor.Acquire(context.Background(), "secret-key", 1)
	if err != nil {
		t.Fatalf("acquire first: %v", err)
	}
	release()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := governor.Acquire(ctx, "secret-key", 1); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected rate-limit wait to honor context deadline, got %v", err)
	}
}

func TestModelGovernorRejectsRequestAboveTokenCapacity(t *testing.T) {
	governor := NewModelGovernor(ModelOptions{
		MaxConcurrent:   1,
		TokensPerPeriod: 10, RatePeriod: time.Minute,
	})
	if _, err := governor.Acquire(context.Background(), "secret-key", 11); !errors.Is(err, ErrTokenLimitExceeded) {
		t.Fatalf("expected token capacity error, got %v", err)
	}
}
