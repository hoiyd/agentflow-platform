package runcommands

import (
	"context"

	"agentflow-platform/apps/api/internal/agent/turn"
	"agentflow-platform/apps/api/internal/approval"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/store"
)

type toolApprovalRuntime interface {
	ResumeToolApproval(context.Context, string, string, string, string) (<-chan domain.RunEvent, <-chan error)
}

func (c Commands) ResumeToolApproval(ctx context.Context, scope Scope, runID, id string, observer Observer) error {
	scoped := c.Store.ForWorkspace(scope.Workspace)
	run, ok, err := scoped.GetRun(runID)
	if err != nil {
		return err
	}
	if !ok {
		return commandError(Missing, store.ErrNotFound("run"))
	}
	runtime, ok := c.Runtime.(toolApprovalRuntime)
	if !ok || scope.Owner == "" {
		return commandError(Conflict, approval.ErrConflict)
	}
	if run.Status != domain.RunWaitingForUser && run.Status != domain.RunFailedRecoverable {
		return commandError(Conflict, approval.ErrConflict)
	}
	reservation, err := c.reserve(nil)
	if err != nil {
		return err
	}
	defer reservation.Cancel()
	release, err := acquire(ctx, reservation, run.ConversationID)
	if err != nil {
		return err
	}
	defer func() { release(); c.afterRelease(ctx, scope, run.ConversationID) }()
	run, _, err = scoped.GetRun(runID)
	if err != nil {
		return err
	}
	if run.Status != domain.RunWaitingForUser && run.Status != domain.RunFailedRecoverable {
		return commandError(Conflict, approval.ErrConflict)
	}
	ctx = context.WithoutCancel(ctx)
	events, errs := runtime.ResumeToolApproval(ctx, scope.Owner, scope.Workspace.ID(), runID, id)
	// Do not open SSE before Runtime accepts the claim. Stale responses remain 409.
	answer, err := consume(events, errs, observer)
	if err != nil {
		if turn.IsWaiting(err) {
			return c.paused(scoped, runID, observer)
		}
		current, _, readErr := scoped.GetRun(runID)
		if readErr != nil {
			return readErr
		}
		if current.Status == domain.RunWaitingForUser || current.Status == domain.RunFailedRecoverable {
			return commandError(Conflict, err)
		}
		return c.fail(scoped, runID, err, current.Status == domain.RunRunning, observer)
	}
	run, _, err = scoped.GetRun(runID)
	if err != nil {
		return err
	}
	if run.Status == domain.RunCanceled || run.Status == domain.RunWaitingForUser {
		terminal(observer, run)
		return nil
	}
	return c.completeClarification(ctx, scoped, run, answer, observer)
}
