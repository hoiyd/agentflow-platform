package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/store"
)

func (r *Runtime) ResumeRecoverableCollaboration(ctx context.Context, runID string) (<-chan domain.RunEvent, <-chan error) {
	events := make(chan domain.RunEvent)
	errs := make(chan error, 1)
	go func() {
		defer close(events)
		defer close(errs)
		run, ok, err := r.store.GetRun(strings.TrimSpace(runID))
		if err != nil {
			errs <- rejectRecovery(err)
			return
		}
		if !ok {
			errs <- rejectRecovery(store.ErrNotFound("run"))
			return
		}
		if run.Status != domain.RunFailedRecoverable {
			errs <- rejectRecovery(errors.New("run is not recoverable"))
			return
		}
		executionCtx, releaseCancellation := r.bindRunCancellation(ctx, run.ID)
		defer releaseCancellation()
		restored, err := r.restoreRuntime(run)
		if err != nil {
			errs <- rejectRecovery(err)
			return
		}
		if restored.mode != ChatModeMultiAgent {
			errs <- rejectRecovery(fmt.Errorf("run %s uses %q mode, not %q", run.ID, restored.mode, ChatModeMultiAgent))
			return
		}
		if err := r.checkpoints.ValidateResume(executionCtx, run); err != nil {
			errs <- rejectRecovery(fmt.Errorf("restore durable checkpoints: %w", err))
			return
		}
		steps, err := r.store.ListCollaborationSteps(run.ID)
		if err != nil {
			errs <- rejectRecovery(err)
			return
		}
		planner, found := latestCompletedCollaborationStep(steps, "planner")
		if !found {
			errs <- rejectRecovery(errors.New("completed planner step not found"))
			return
		}
		router, found := latestCompletedCollaborationStep(steps, "router")
		task, err := r.taskWithClarifications(run.ID, planner.Input)
		if err != nil {
			errs <- rejectRecovery(err)
			return
		}
		if !found {
			errs <- rejectRecovery(errors.New("completed router step not found; recover from the plan approval boundary"))
			return
		}
		workerAgent, found := findAgentByID(restored.candidateAgents, router.AgentID)
		if !found {
			errs <- rejectRecovery(errors.New("frozen routed worker not found"))
			return
		}
		reason, err := r.persistedSelectionReason(run.ID, router)
		if err != nil {
			errs <- rejectRecovery(err)
			return
		}
		run, err = r.transitionRun(run, domain.RunRunning, domain.EventRunResumed, "", nil)
		if err != nil {
			errs <- err
			return
		}
		if _, err := r.checkpoints.RestoreRun(executionCtx, run); err != nil {
			errs <- fmt.Errorf("restore durable checkpoints: %w", err)
			return
		}
		events <- liveRunEvent(run)
		prepared := PreparedCollaborationRun{WorkerAgent: workerAgent, Run: run}
		if err := r.runCollaborationTail(executionCtx, events, prepared, restored.catalog, task, planner.Output, reason, steps); err != nil {
			errs <- err
		}
	}()
	return events, errs
}

func (r *Runtime) persistedSelectionReason(runID string, router domain.CollaborationStep) (string, error) {
	events, err := r.store.ListRunEvents(runID)
	if err != nil {
		return "", err
	}
	for i := len(events) - 1; i >= 0; i-- {
		item := events[i]
		if item.Type == domain.EventAgentSelectionDecided && item.StageID == router.ID && item.Payload["selected_agent_id"] == router.AgentID {
			reason, _ := item.Payload["reason"].(string)
			return reason, nil
		}
	}
	// Older records without typed selection evidence retain the frozen worker,
	// but cannot supply a reason. Do not parse display text or reroute.
	return "", nil
}
