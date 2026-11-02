package runcommands

import (
	"context"

	"agentflow-platform/apps/api/app/runcompletion"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/store"
)

func (c Commands) completeClarification(ctx context.Context, scoped store.WorkspaceStore, run domain.Run, answer string, observer Observer) error {
	chunk, err := runcompletion.CompleteClarification(ctx, scoped, runcompletion.Dependencies{Runtime: c.Runtime, Verification: c.Verification}, run, answer)
	if err != nil {
		return err
	}
	if observer.Done != nil {
		observer.Done(chunk)
	}
	return nil
}
