package agent

import (
	"context"
	"errors"

	"agentflow-platform/apps/api/internal/domain"
)

// A consumed follow-up is already running durably, but has no worker yet. If
// Stop wins startup, this rejected starter must acknowledge its cancellation.
func (r *Runtime) startRun(run domain.Run) (domain.Run, error) {
	current, err := r.transitionRun(run, domain.RunRunning, domain.EventRunStarted, "", nil)
	if !errors.Is(err, ErrRunStateChanged) || current.Status != domain.RunCanceling {
		return current, err
	}
	settled, settleErr := r.FailRun(run.ID, context.Canceled)
	if settleErr != nil {
		return current, errors.Join(err, settleErr)
	}
	return settled, err
}
