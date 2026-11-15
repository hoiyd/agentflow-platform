package runcommands

import (
	"context"

	"agentflow-platform/apps/api/app/runcompletion"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/store"
)

func (c Commands) completeResumedTurn(ctx context.Context, scoped store.WorkspaceStore, runID, answer string, observer Observer) error {
	run, _, err := scoped.GetRun(runID)
	if err != nil {
		return err
	}
	if run.Status == domain.RunWaitingForUser || run.Status == domain.RunCanceled {
		terminal(observer, run)
		return nil
	}
	chunk, err := runcompletion.CompleteResumedTurn(ctx, scoped, runcompletion.Dependencies{Runtime: c.Runtime, Verification: c.Verification}, run, answer)
	if err != nil {
		return err
	}
	if observer.Done != nil {
		observer.Done(chunk)
	}
	return nil
}
