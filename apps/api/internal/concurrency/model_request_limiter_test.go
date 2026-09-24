package concurrency

import (
	"context"
	"errors"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/inference/requestcontrol"
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
		var limitErr *requestcontrol.TokenBucketCapacityError
		if !errors.As(err, &limitErr) || limitErr.EstimatedTokens != 11 || limitErr.Capacity != 10 {
			t.Fatalf("unexpected token capacity error: %#v", err)
		}
	}
}

func TestModelRequestLimiterSeparatesRateAndPermitWait(t *testing.T) {
	limiter := NewModelRequestLimiter(ModelRequestLimits{MaxConcurrent: 1, RequestsPerPeriod: 1, RatePeriod: 80 * time.Millisecond})
	first, err := limiter.AcquireRequest(context.Background(), "key", 1)
	if err != nil {
		t.Fatal(err)
	}
	defer first()
	timing := &requestcontrol.AttemptTiming{}
	ctx := requestcontrol.WithAttemptTiming(context.Background(), timing)
	acquired := make(chan func(), 1)
	go func() {
		release, err := limiter.AcquireRequest(ctx, "key", 1)
		if err == nil {
			acquired <- release
		}
	}()
	time.Sleep(105 * time.Millisecond)
	first()
	select {
	case release := <-acquired:
		release()
	case <-time.After(time.Second):
		t.Fatal("waiter did not recover after capacity was released")
	}
	if !timing.Limited || timing.RateWait < 60*time.Millisecond || timing.PermitWait < 20*time.Millisecond {
		t.Fatalf("local waits were not measured independently: %#v", timing)
	}
}

func TestModelRequestLimiterMeasuresTPMWait(t *testing.T) {
	limiter := NewModelRequestLimiter(ModelRequestLimits{MaxConcurrent: 1, TokensPerPeriod: 10, RatePeriod: 100 * time.Millisecond})
	first, err := limiter.AcquireRequest(context.Background(), "key", 8)
	if err != nil {
		t.Fatal(err)
	}
	first()
	timing := &requestcontrol.AttemptTiming{}
	second, err := limiter.AcquireRequest(requestcontrol.WithAttemptTiming(context.Background(), timing), "key", 8)
	if err != nil {
		t.Fatal(err)
	}
	second()
	if timing.RateWait < 40*time.Millisecond || timing.PermitWait > 20*time.Millisecond {
		t.Fatalf("TPM refill was not separated from the free permit: %#v", timing)
	}
}

func TestCanceledPermitWaitCannotStealReleasedCapacity(t *testing.T) {
	limiter := NewModelRequestLimiter(ModelRequestLimits{MaxConcurrent: 1})
	first, err := limiter.AcquireRequest(context.Background(), "", 1)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := limiter.AcquireRequest(ctx, "", 1)
		done <- err
	}()
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled waiter acquired capacity: %v", err)
	}
	first()
	probeCtx, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	probe, err := limiter.AcquireRequest(probeCtx, "", 1)
	if err != nil {
		t.Fatalf("permit leaked after cancellation: %v", err)
	}
	probe()
}
