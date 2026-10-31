package httpapi

import (
	"context"
	"errors"
	"log"

	"agentflow-platform/apps/api/app/runcommands"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/store"
)

func (h *Handler) scheduleFollowup(ctx context.Context, scope runcommands.Scope, conversation string, allowStopped bool) {
	if h.inbox == nil {
		return
	}
	if err := h.dispatchFollowup(ctx, scope, conversation, allowStopped); err != nil && !errors.Is(err, store.ErrInputConflict) {
		// No retry loop: retained receipts are visible and can be explicitly retried.
		log.Printf("follow-up dispatch deferred: %v", err)
	}
}

func (h *Handler) dispatchFollowup(ctx context.Context, scope runcommands.Scope, conversation string, allowStopped bool) error {
	ctx = context.WithoutCancel(ctx)
	items, err := h.inbox.ListRunInputs(ctx, scope.Owner, scope.Workspace.ID(), conversation)
	if err != nil {
		return err
	}
	var input *domain.RunInput
	for i := range items {
		if items[i].Kind == "follow_up" && items[i].Status == "queued" {
			input = &items[i]
			break
		}
	}
	if input == nil {
		return store.ErrInputConflict
	}
	source, found, err := h.store.ForWorkspace(scope.Workspace).GetRun(input.RunID)
	if err != nil {
		return err
	}
	if !found || source.ConversationID != conversation {
		return store.ErrInputConflict
	}
	if source.RuntimeSnapshot != nil {
		if err := store.ValidateFollowupIntent(source.RuntimeSnapshot.ExecutionConstraint); err != nil {
			return err
		}
	}
	runs, err := h.store.ForWorkspace(scope.Workspace).ListRuns()
	if err != nil {
		return err
	}
	eligible := false
	for _, run := range runs {
		if run.ConversationID == conversation {
			eligible = run.Status == domain.RunCompleted || allowStopped && (run.Status == domain.RunFailed || run.Status == domain.RunCanceled)
			break
		}
	}
	if !eligible {
		return store.ErrInputConflict
	}
	// Reserve before spawning: pending workers are bounded by existing admission
	// and participate in application shutdown. The command reacquires the same writer.
	reservation, err := h.runController.Reserve()
	if err != nil {
		return err
	}
	go func() {
		defer reservation.Cancel()
		err := h.runCommands().Start(ctx, scope, domain.ChatRequest{ConversationID: conversation, WorkspaceID: scope.Workspace.ID(), AgentID: input.AgentID, Mode: input.Mode, Message: input.Content}, reservation, &runcommands.Followup{Input: *input, AllowStopped: allowStopped}, runcommands.Observer{})
		if err != nil {
			log.Printf("follow-up execution deferred: %v", err)
		}
	}()
	return nil
}
