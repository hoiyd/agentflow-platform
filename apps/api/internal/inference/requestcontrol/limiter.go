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
	OwnerLimited       bool
	OwnerWait          time.Duration
	TransportStartedAt time.Time
}

type ownerKey struct{}

// WithOwner binds a server-verified identity, never a caller-supplied header or
// Tool argument. Detached contexts retain it; background jobs resolve the Run.
func WithOwner(ctx context.Context, owner string) context.Context {
	return context.WithValue(ctx, ownerKey{}, owner)
}

func OwnerFromContext(ctx context.Context) string {
	owner, _ := ctx.Value(ownerKey{}).(string)
	return owner
}

// OwnerAdmissionError is local overload, not a provider failure. Clients may
// retry a new operation, but provider retries must not amplify a full local queue.
type OwnerAdmissionError struct{ Code string }

func (e *OwnerAdmissionError) Error() string {
	switch e.Code {
	case "model_owner_required":
		return "a trusted owner is required for model requests"
	case "model_owner_unavailable":
		return "model request owner no longer has access to the Run's Workspace"
	case "owner_model_queue_full":
		return "owner model request capacity is full; try again shortly"
	case "owner_model_queue_timeout":
		return "owner model capacity wait timed out; try again shortly"
	case "model_limiter_closed":
		return "model request admission is shutting down"
	default:
		return "model request owner could not be resolved"
	}
}

func (e *OwnerAdmissionError) FailureInfo() failure.Info {
	info := failure.Info{Code: e.Code, Source: "model_request_limiter", Category: failure.CategoryCapacity}
	switch e.Code {
	case "model_owner_required", "model_owner_unavailable":
		info.Category = failure.CategoryAuthentication
	case "model_owner_resolution_failed":
		info.Category = failure.CategoryAvailability
		info.Retryable = true
		info.Details = map[string]any{"retry_after_ms": int64(1000)}
	default:
		info.Retryable = true
		info.Details = map[string]any{"retry_after_ms": int64(1000)}
	}
	return info
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
