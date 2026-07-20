package modelrequest

import (
	"context"
	"fmt"
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
