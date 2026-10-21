package app

import (
	"context"

	"agentflow-platform/apps/api/internal/inference/requestcontrol"
)

type Option func(*applicationOptions)
type applicationOptions struct{ requestGuard requestcontrol.Limiter }

// WithModelRequestGuard adds a process-hosted request ceiling without replacing
// production owner/key/global admission. Embedding clients retain their own path.
func WithModelRequestGuard(guard requestcontrol.Limiter) Option {
	return func(options *applicationOptions) { options.requestGuard = guard }
}

type guardedModelRequests struct{ base, guard requestcontrol.Limiter }

func (g guardedModelRequests) AcquireRequest(ctx context.Context, key string, tokens int) (func(), error) {
	release, err := g.base.AcquireRequest(ctx, key, tokens)
	if err != nil {
		return nil, err
	}
	extra, err := g.guard.AcquireRequest(ctx, key, tokens)
	if err != nil {
		release()
		return nil, err
	}
	return func() { extra(); release() }, nil
}
