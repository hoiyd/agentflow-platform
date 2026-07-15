package modelrequest

import (
	"context"
	"fmt"
)

// Limiter controls admission for one physical model HTTP request.
type Limiter interface {
	AcquireRequest(ctx context.Context, apiKey string, estimatedTokens int) (release func(), err error)
}

// TokenLimitError reports that one request cannot fit within the configured
// local token-bucket capacity.
type TokenLimitError struct {
	EstimatedTokens int
	Capacity        int
}

func (e *TokenLimitError) Error() string {
	return fmt.Sprintf(
		"estimated request tokens exceed the configured token bucket capacity: estimated=%d capacity=%d",
		e.EstimatedTokens,
		e.Capacity,
	)
}
