package contexteval

import (
	"context"
	"sync"

	"agentflow-platform/apps/api/internal/failure"
)

// Ledger entries count logical operations; transport compatibility fallbacks
// can send again without a new operation. The existing physical-request hook
// enforces the same suite call ceiling across both paths. Token settlement
// remains exclusively owned by the production Usage Ledger.
type liveRequestBudget struct {
	mu   sync.Mutex
	max  int
	used int
}

func (b *liveRequestBudget) AcquireRequest(ctx context.Context, _ string, _ int) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.used >= b.max {
		return nil, failure.New(failure.Definition{Message: "evaluation physical model-request budget exhausted", Info: failure.Info{Code: "evaluation_request_budget_exhausted", Source: "context_evaluation", Category: failure.CategoryCapacity}})
	}
	b.used++
	return func() {}, nil
}

func (b *liveRequestBudget) count() int { b.mu.Lock(); defer b.mu.Unlock(); return b.used }
