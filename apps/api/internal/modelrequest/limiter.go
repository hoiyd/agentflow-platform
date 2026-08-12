package modelrequest

import (
	"context"
	"fmt"

	"agentflow-platform/apps/api/internal/failure"
)

// Limiter controls admission for one physical model HTTP request.
type Limiter interface {
	AcquireRequest(ctx context.Context, apiKey string, estimatedTokens int) (release func(), err error)
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
