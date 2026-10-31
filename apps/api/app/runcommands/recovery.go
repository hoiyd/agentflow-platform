package runcommands

import (
	"context"
	"errors"
	"strings"

	"agentflow-platform/apps/api/app/runcompletion"
	"agentflow-platform/apps/api/internal/agent"
	"agentflow-platform/apps/api/internal/checkpoint"
	"agentflow-platform/apps/api/internal/clarification"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/rag/eligibility"
	"agentflow-platform/apps/api/internal/redaction"
	"agentflow-platform/apps/api/internal/store"
)

func (c Commands) Continue(ctx context.Context, scope Scope, id, plan string, routing domain.AgentRoutingRequirements, observer Observer) error {
	if err := redaction.ValidateValue(struct {
		Plan    string
		Routing domain.AgentRoutingRequirements
	}{plan, routing}); err != nil {
		return commandError(Invalid, err)
	}
	return c.recover(ctx, scope, id, false, "", plan, routing, observer)
}

func (c Commands) Resume(ctx context.Context, scope Scope, id, input string, observer Observer) error {
	if err := redaction.ValidateValue(input); err != nil {
		return commandError(Invalid, err)
	}
	return c.recover(ctx, scope, id, true, strings.TrimSpace(input), "", domain.AgentRoutingRequirements{}, observer)
}

func (c Commands) recover(ctx context.Context, scope Scope, id string, resume bool, input, plan string, routing domain.AgentRoutingRequirements, observer Observer) error {
	if scope.Owner == "" {
		return commandError(Invalid, errors.New("authorized owner is required"))
	}
	scoped := c.Store.ForWorkspace(scope.Workspace)
	run, found, err := scoped.GetRun(id)
	if err != nil {
		return err
	}
	if !found {
		return commandError(Missing, store.ErrNotFound("run"))
	}
	if resume {
		if _, err := c.resumeRoute(ctx, scope, run, input); err != nil {
			return err
		}
		effects, err := scoped.ListToolEffects(id)
		if err != nil {
			return err
		}
		for _, effect := range effects {
			if domain.ToolEffectRequiresReconciliation(effect.Status) {
				return commandError(Conflict, errors.New("run has unresolved tool effects; reconcile them before resuming"))
			}
		}
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
	// A queued command must route using current durable state, not its first read.
	run, found, err = scoped.GetRun(id)
	if err != nil {
		return err
	}
	if !found {
		return commandError(Missing, store.ErrNotFound("run"))
	}
	if resume {
		questionID, err := c.resumeRoute(ctx, scope, run, input)
		if err != nil {
			return err
		}
		if questionID != "" {
			return c.resumeClarification(ctx, scope, scoped, run, questionID, observer)
		}
	}
	if observer.Run != nil {
		observer.Run(run)
	}
	ctx = context.WithoutCancel(ctx)
	var events <-chan domain.RunEvent
	var errs <-chan error
	if !resume {
		events, errs = c.Runtime.ContinueCollaboration(ctx, id, plan, routing)
	} else if run.Status != domain.RunFailedRecoverable {
		events, errs = c.Runtime.ResumeAutonomous(ctx, id, input)
	} else if run.RuntimeSnapshot != nil && run.RuntimeSnapshot.Mode == agent.ChatModeMultiAgent {
		events, errs = c.Runtime.ResumeRecoverableCollaboration(ctx, id)
	} else {
		events, errs = c.Runtime.ResumeRecoverableAutonomous(ctx, id, input)
	}
	answer, err := consume(events, errs, observer)
	if err != nil {
		if errors.Is(err, clarification.Pending) {
			return c.paused(scoped, id, observer)
		}
		reason, failRun := ContinuationFailurePolicy(err)
		if resume {
			reason, failRun = ResumeFailurePolicy(err)
		}
		return c.fail(scoped, id, commandError(reason, err), failRun, observer)
	}
	if resume {
		run, found, err = scoped.GetRun(id)
		if err != nil {
			_, _ = c.Runtime.FailRun(id, err)
			return err
		}
		if !found {
			return commandError(Missing, store.ErrNotFound("run"))
		}
		if run.Status == domain.RunWaitingForUser || run.Status == domain.RunCanceled {
			terminal(observer, run)
			return nil
		}
	}
	return c.complete(ctx, scoped, runcompletion.Request{WorkspaceID: run.WorkspaceID, RunID: id, ConversationID: run.ConversationID, Assistant: answer}, observer)
}

func (c Commands) resumeRoute(ctx context.Context, scope Scope, run domain.Run, input string) (string, error) {
	if run.Status != domain.RunWaitingForUser && run.Status != domain.RunFailedRecoverable {
		return "", commandError(Conflict, errors.New("run is not resumable in its current state"))
	}
	if c.Clarifications != nil {
		items, err := c.Clarifications.ListClarifications(ctx, scope.Owner, scope.Workspace.ID(), run.ID)
		if err != nil {
			return "", err
		}
		if len(items) > 0 {
			latest := items[len(items)-1]
			switch latest.Status {
			case "pending", "expired":
				return "", commandError(Conflict, clarification.ErrConflict)
			case "answered":
				if run.Status != domain.RunWaitingForUser {
					return "", commandError(Conflict, clarification.ErrConflict)
				}
				return latest.ID, nil
			case "applied":
				if run.Status == domain.RunFailedRecoverable {
					return latest.ID, nil
				}
			}
		}
	}
	if run.RuntimeSnapshot == nil {
		return "", commandError(Conflict, agent.ErrRuntimeSnapshotUnavailable)
	}
	mode := run.RuntimeSnapshot.Mode
	if mode != agent.ChatModeAutonomous && (mode != agent.ChatModeMultiAgent || run.Status != domain.RunFailedRecoverable) {
		return "", commandError(Conflict, errors.New("run mode does not support ordinary resume; use its bound continuation"))
	}
	if run.Status == domain.RunWaitingForUser && input == "" {
		return "", commandError(Invalid, errors.New("user input is required"))
	}
	return "", nil
}

func ContinuationFailurePolicy(err error) (Reason, bool) {
	var rejected *agent.PreExecutionRejection
	if errors.As(err, &rejected) || errors.Is(err, agent.ErrRunStateChanged) {
		return Conflict, false
	}
	if errors.Is(err, agent.ErrNoEligibleAgent) || errors.Is(err, agent.ErrNoSuitableAgent) || errors.Is(err, agent.ErrInvalidRoutingRequirements) {
		return Routing, true
	}
	if errors.Is(err, eligibility.ErrInvalid) || errors.Is(err, eligibility.ErrSourceUnavailable) || errors.Is(err, agent.ErrRuntimeSnapshotUnavailable) {
		return Conflict, true
	}
	return "", true
}

func ResumeFailurePolicy(err error) (Reason, bool) {
	var rejected *agent.PreExecutionRejection
	if errors.As(err, &rejected) || errors.Is(err, agent.ErrRunStateChanged) {
		return Conflict, false
	}
	if errors.Is(err, eligibility.ErrInvalid) || errors.Is(err, eligibility.ErrSourceUnavailable) || errors.Is(err, agent.ErrRuntimeSnapshotResumeUnsupported) || errors.Is(err, agent.ErrRuntimeSnapshotUnavailable) || errors.Is(err, agent.ErrRuntimeExecutorUnsupported) || errors.Is(err, checkpoint.ErrNeedsReconciliation) || errors.Is(err, checkpoint.ErrCheckpointStale) {
		return Conflict, true
	}
	return "", true
}
