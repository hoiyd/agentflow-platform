package requestcontrol

import (
	"context"
	"fmt"
	"time"

	"agentflow-platform/apps/api/internal/failure"
)

// Limiter controls admission for one physical model HTTP request.
type Limiter interface {
	AcquireRequest(ctx context.Context, apiKey string, estimatedTokens int) (release func(), err error)
}

// AttemptTiming is scoped to one physical request; these are local client-side
// boundaries, not provider queue or inference-phase measurements.
type AttemptTiming struct {
	Limited            bool
	RateWait           time.Duration
	PermitWait         time.Duration
	TransportStartedAt time.Time
}

type attemptTimingKey struct{}

func WithAttemptTiming(ctx context.Context, timing *AttemptTiming) context.Context {
	return context.WithValue(ctx, attemptTimingKey{}, timing)
}

func AttemptTimingFromContext(ctx context.Context) *AttemptTiming {
	timing, _ := ctx.Value(attemptTimingKey{}).(*AttemptTiming)
	return timing
}

// TokenBucketCapacityError reports that one physical request cannot fit within
// the configured per-key TPM bucket capacity.
type TokenBucketCapacityError struct {
	EstimatedTokens int
	Capacity        int
}

func (e *TokenBucketCapacityError) Error() string {
	return fmt.Sprintf(
		"estimated request tokens exceed the configured token bucket capacity: estimated=%d capacity=%d",
		e.EstimatedTokens,
		e.Capacity,
	)
}

func (e *TokenBucketCapacityError) FailureInfo() failure.Info {
	if e == nil {
		return failure.Info{Code: "request_token_capacity_exceeded", Source: "model_request_limiter", Category: failure.CategoryCapacity}
	}
	return failure.Info{
		Code: "request_token_capacity_exceeded", Source: "model_request_limiter", Category: failure.CategoryCapacity,
		Details: map[string]any{"estimated_tokens": e.EstimatedTokens, "capacity": e.Capacity},
	}
}
