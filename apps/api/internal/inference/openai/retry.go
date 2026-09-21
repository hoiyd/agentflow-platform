package openai

import (
	"context"
	"log"
	"math/rand/v2"
	"time"
)

const (
	DefaultModelRetryMaxAttempts = 3
	DefaultModelRetryBaseDelay   = 500 * time.Millisecond
	DefaultModelRetryMaxDelay    = 5 * time.Second
	defaultModelRetryJitter      = 0.2
)

// RetryPolicy controls bounded retries for transient model failures.
type RetryPolicy struct {
	// MaxAttempts includes the initial request.
	MaxAttempts int
	// BaseDelay is the delay after the first retryable failure.
	BaseDelay time.Duration
	// MaxDelay caps exponential backoff and Retry-After.
	MaxDelay time.Duration
	// Jitter is a fractional range from 0 to 1 applied to backoff.
	Jitter float64
}

// DefaultRetryPolicy returns the production retry defaults.
func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{
		MaxAttempts: DefaultModelRetryMaxAttempts,
		BaseDelay:   DefaultModelRetryBaseDelay,
		MaxDelay:    DefaultModelRetryMaxDelay,
		Jitter:      defaultModelRetryJitter,
	}
}

func executeWithRetry[T any](ctx context.Context, policy RetryPolicy, operation string, attempt func() (T, error)) (T, error) {
	policy = policy.normalized()
	var zero T
	for attemptNumber := 1; attemptNumber <= policy.MaxAttempts; attemptNumber++ {
		value, err := attempt()
		if err == nil {
			return value, nil
		}
		modelErr := classifyModelError(operation, err)
		modelErr.Attempts = attemptNumber
		if ctx.Err() != nil {
			canceled := classifyModelError(operation, ctx.Err())
			canceled.Attempts = attemptNumber
			return value, canceled
		}
		if !modelErr.Retryable || attemptNumber == policy.MaxAttempts {
			return value, modelErr
		}
		delay := policy.delay(attemptNumber, modelErr.RetryAfter)
		log.Printf(
			"model_request_retry operation=%s kind=%s attempt=%d max_attempts=%d delay_ms=%d status=%d provider_code=%q",
			operation, modelErr.Kind, attemptNumber, policy.MaxAttempts, delay.Milliseconds(), modelErr.StatusCode, modelErr.ProviderCode,
		)
		if err := waitForRetry(ctx, delay); err != nil {
			canceled := classifyModelError(operation, err)
			canceled.Attempts = attemptNumber
			return value, canceled
		}
	}
	return zero, &ModelError{Kind: ErrorTransport, Operation: operation, Message: "retry policy exhausted"}
}

func (p RetryPolicy) normalized() RetryPolicy {
	defaults := DefaultRetryPolicy()
	if p.MaxAttempts <= 0 {
		p.MaxAttempts = defaults.MaxAttempts
	}
	if p.BaseDelay <= 0 {
		p.BaseDelay = defaults.BaseDelay
	}
	if p.MaxDelay <= 0 {
		p.MaxDelay = defaults.MaxDelay
	}
	if p.MaxDelay < p.BaseDelay {
		p.MaxDelay = p.BaseDelay
	}
	if p.Jitter < 0 {
		p.Jitter = 0
	}
	if p.Jitter > 1 {
		p.Jitter = 1
	}
	return p
}

func (p RetryPolicy) delay(attemptNumber int, retryAfter time.Duration) time.Duration {
	p = p.normalized()
	delay := p.BaseDelay
	for current := 1; current < attemptNumber && delay < p.MaxDelay; current++ {
		if delay > p.MaxDelay/2 {
			delay = p.MaxDelay
			break
		}
		delay *= 2
	}
	if p.Jitter > 0 {
		factor := 1 + ((rand.Float64()*2)-1)*p.Jitter
		delay = time.Duration(float64(delay) * factor)
	}
	if retryAfter > delay {
		delay = retryAfter
	}
	if delay > p.MaxDelay {
		return p.MaxDelay
	}
	return delay
}

func waitForRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
