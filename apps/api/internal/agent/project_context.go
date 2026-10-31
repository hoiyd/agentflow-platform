package agent

import (
	"context"
	"fmt"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/projectguidance"
)

type ProjectGuidanceProvider interface {
	Freeze(context.Context, string, domain.ProjectContext) (*domain.ProjectGuidanceSnapshot, error)
	Validate(context.Context, string, *domain.ProjectGuidanceSnapshot) error
}

type projectContextKey struct{}

// Only new Run preparation consumes this selection. Resume uses the persisted
// text and selection instead of reinterpreting request drafts or live files.
func WithProjectContext(ctx context.Context, selection *domain.ProjectContext) context.Context {
	return context.WithValue(ctx, projectContextKey{}, selection)
}

func (r *Runtime) freezeProjectGuidance(ctx context.Context, conversation string, snapshot *domain.RuntimeSnapshot) error {
	selection, _ := ctx.Value(projectContextKey{}).(*domain.ProjectContext)
	if selection == nil {
		return nil
	}
	if r.projectGuidance == nil {
		return fmt.Errorf("project instructions are unavailable")
	}
	owner, ok, err := r.store.GetConversation(conversation)
	if err != nil {
		return err
	}
	if !ok || owner.WorkspaceID == "" {
		return fmt.Errorf("project instructions require a persisted Workspace Conversation")
	}
	snapshot.ProjectGuidance, err = r.projectGuidance.Freeze(ctx, owner.WorkspaceID, *selection)
	return err
}

func (r *Runtime) validateProjectGuidance(ctx context.Context, run domain.Run) error {
	if run.RuntimeSnapshot == nil || run.RuntimeSnapshot.ProjectGuidance == nil {
		return nil
	}
	frozen := run.RuntimeSnapshot.ProjectGuidance
	if err := projectguidance.ValidateSnapshot(frozen); err != nil {
		return err
	}
	if frozen.WorkspaceID != run.WorkspaceID || r.projectGuidance == nil {
		return fmt.Errorf("frozen project instructions authority is unavailable")
	}
	return r.projectGuidance.Validate(ctx, run.WorkspaceID, frozen)
}
