package runcommands

import (
	"context"
	"errors"

	"agentflow-platform/apps/api/internal/clarification"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/store"
)

type clarificationRuntime interface {
	ResumeClarification(context.Context, string, string, string, string) (<-chan domain.RunEvent, <-chan error)
}

func (c Commands) ResumeClarification(ctx context.Context, scope Scope, runID, id string, observer Observer) error {
	if scope.Owner == "" {
		return commandError(Invalid, clarification.ErrInvalid)
	}
	scoped := c.Store.ForWorkspace(scope.Workspace)
	run, ok, err := scoped.GetRun(runID)
	if err != nil {
		return err
	}
	if !ok {
		return commandError(Missing, store.ErrNotFound("run"))
	}
	if run.Status != domain.RunWaitingForUser && run.Status != domain.RunFailedRecoverable {
		return commandError(Conflict, clarification.ErrConflict)
	}
	runtime, ok := c.Runtime.(clarificationRuntime)
	if !ok {
		return commandError(Conflict, clarification.ErrConflict)
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
	// Re-read after single-writer acquisition: a queued duplicate cannot restart.
	run, _, err = scoped.GetRun(runID)
	if err != nil {
		return err
	}
	if run.Status != domain.RunWaitingForUser && run.Status != domain.RunFailedRecoverable {
		return commandError(Conflict, clarification.ErrConflict)
	}
	ctx = context.WithoutCancel(ctx)
	events, errs := runtime.ResumeClarification(ctx, scope.Owner, scope.Workspace.ID(), runID, id)
	if observer.Run != nil {
		observer.Run(run)
	}
	answer, err := consume(events, errs, observer)
	if err != nil {
		if errors.Is(err, clarification.Pending) {
			return c.paused(scoped, runID, observer)
		}
		current, _, loadErr := scoped.GetRun(runID)
		if loadErr != nil {
			return loadErr
		}
		if current.Status == domain.RunWaitingForUser || current.Status == domain.RunFailedRecoverable {
			return commandError(Conflict, err)
		}
		reason, failRun := ResumeFailurePolicy(err)
		if current.Status == domain.RunRunning {
			failRun = true
		}
		return c.fail(scoped, runID, commandError(reason, err), failRun, observer)
	}
	current, _, err := scoped.GetRun(runID)
	if err != nil {
		return err
	}
	if current.Status == domain.RunWaitingForUser || current.Status == domain.RunCanceled {
		terminal(observer, current)
		return nil
	}
	return c.completeClarification(ctx, scoped, current, answer, observer)
}
