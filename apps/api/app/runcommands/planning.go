package runcommands

import (
	"context"
	"errors"
	"fmt"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/planning"
	"agentflow-platform/apps/api/internal/store"
)

// resolvePlan runs inside Conversation admission, before messages or a new Run.
// Plan approval selects intent, never grants Tool authority.
func (c Commands) resolvePlan(ctx context.Context, scoped store.WorkspaceStore, req *domain.ChatRequest, conversation string) error {
	if req.PlanOrigin == nil {
		return nil
	}
	run, found, err := scoped.GetRun(req.PlanOrigin.RunID)
	if err != nil {
		return err
	}
	if !found || run.ConversationID != conversation {
		return commandError(Missing, store.ErrNotFound("plan Run"))
	}
	if _, found, err := scoped.GetRun(planning.ExecutionRunID(run.ID)); err != nil {
		return err
	} else if found {
		return commandError(Conflict, fmt.Errorf("%w: %s", planning.ErrAlreadyStarted, planning.ExecutionRunID(run.ID)))
	}
	items, err := scoped.ListRunEvents(run.ID)
	if err != nil {
		return err
	}
	plan := planning.Read(items)
	if err := planning.Validate(run, plan, req.PlanOrigin.Hash, time.Now()); err != nil {
		return commandError(Conflict, err)
	}
	if req.AgentID != "" && req.AgentID != run.RuntimeSnapshot.Agent.ID {
		return commandError(Invalid, errors.New("plan execution must use the same owning Agent; its current configuration is reauthorized"))
	}
	validator, ok := c.Runtime.(interface {
		ValidatePlanSources(context.Context, domain.Run, []domain.PlanSource) error
	})
	if !ok {
		return commandError(Conflict, errors.New("plan source validation unavailable"))
	}
	if err := validator.ValidatePlanSources(ctx, run, plan.Sources); err != nil {
		return commandError(Conflict, err)
	}
	req.AgentID, req.Mode = run.RuntimeSnapshot.Agent.ID, run.RuntimeSnapshot.Mode
	req.Message = "Execute the reviewed plan below within current authorization. Approval is not permission to bypass Tool policy. Recheck source hashes before changes.\nOriginal input:\n" + plan.Input + "\nReviewed proposal:\n" + plan.Proposal
	return nil
}
