package agent

import (
	"context"
	"time"

	"agentflow-platform/apps/api/internal/budget"
)

func (r *Runtime) contextWithRunBudget(ctx context.Context, runID string) (context.Context, context.CancelFunc, error) {
	run, ok, err := r.store.GetRun(runID)
	if err != nil {
		return ctx, func() {}, err
	}
	if !ok {
		return ctx, func() {}, ErrRuntimeSnapshotUnavailable
	}
	if run.RuntimeSnapshot == nil || run.RuntimeSnapshot.RunBudget == nil {
		return ctx, func() {}, nil
	}
	tracker := budget.NewTracker(r.store, r.runEventSink(), run)
	ctx = budget.WithController(ctx, tracker)
	remaining, limited := tracker.RemainingRuntime(time.Now().UTC())
	if !limited {
		return ctx, func() {}, nil
	}
	cause := &budget.ExceededError{
		Resource: budget.ResourceRuntime,
		Limit:    run.RuntimeSnapshot.RunBudget.MaxRuntimeMS,
		Used:     budget.ActiveRuntimeMS(run, time.Now().UTC()),
	}
	if remaining <= 0 {
		tracker.PublishExceeded(context.WithoutCancel(ctx), cause)
		limitedCtx, cancel := context.WithCancelCause(ctx)
		cancel(cause)
		return limitedCtx, func() {}, cause
	}
	limitedCtx, cancel := context.WithTimeoutCause(ctx, remaining, cause)
	return limitedCtx, cancel, nil
}

func runBudgetCause(ctx context.Context, err error) error {
	if cause := context.Cause(ctx); cause != nil {
		if _, ok := budget.AsExceeded(cause); ok {
			if tracker, ok := budget.FromContext(ctx).(*budget.Tracker); ok {
				tracker.PublishExceeded(context.WithoutCancel(ctx), cause)
			}
			return cause
		}
	}
	return err
}
