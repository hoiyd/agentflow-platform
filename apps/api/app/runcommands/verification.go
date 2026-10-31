package runcommands

import (
	"context"
	"errors"

	"agentflow-platform/apps/api/app/runcompletion"
	"agentflow-platform/apps/api/internal/store"
)

// Reverify shares Conversation admission with execution and recovery so a
// passing check cannot settle a candidate replaced by a concurrent Resume.
func (c Commands) Reverify(ctx context.Context, scope Scope, id string) (runcompletion.VerificationResult, error) {
	if scope.Owner == "" {
		return runcompletion.VerificationResult{}, commandError(Invalid, errors.New("authorized owner is required"))
	}
	scoped := c.Store.ForWorkspace(scope.Workspace)
	run, found, err := scoped.GetRun(id)
	if err != nil {
		return runcompletion.VerificationResult{}, err
	}
	if !found {
		return runcompletion.VerificationResult{}, commandError(Missing, store.ErrNotFound("run"))
	}
	reservation, err := c.reserve(nil)
	if err != nil {
		return runcompletion.VerificationResult{}, err
	}
	defer reservation.Cancel()
	release, err := acquire(ctx, reservation, run.ConversationID)
	if err != nil {
		return runcompletion.VerificationResult{}, err
	}
	defer func() { release(); c.afterRelease(ctx, scope, run.ConversationID) }()
	// Both Run status and candidate may have changed while admission was queued.
	run, found, err = scoped.GetRun(id)
	if err != nil {
		return runcompletion.VerificationResult{}, err
	}
	if !found {
		return runcompletion.VerificationResult{}, commandError(Missing, store.ErrNotFound("run"))
	}
	return runcompletion.Reverify(ctx, scoped, runcompletion.Dependencies{Runtime: c.Runtime, Verification: c.Verification}, run)
}
