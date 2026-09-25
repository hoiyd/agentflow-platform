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
			errs <- err
			return
		}
		if !ok {
			errs <- store.ErrNotFound("run")
			return
		}
		if run.Status != domain.RunFailedRecoverable {
			errs <- errors.New("run is not recoverable")
			return
		}
		executionCtx, releaseCancellation := r.bindRunCancellation(ctx, run.ID)
		defer releaseCancellation()
		restored, err := r.restoreRuntime(run)
		if err != nil {
			errs <- err
			return
		}
		if restored.mode != ChatModeMultiAgent {
			errs <- fmt.Errorf("run %s uses %q mode, not %q", run.ID, restored.mode, ChatModeMultiAgent)
			return
		}
		if _, err := r.checkpoints.RestoreRun(executionCtx, run); err != nil {
			errs <- fmt.Errorf("restore durable checkpoints: %w", err)
			return
		}
		steps, err := r.store.ListCollaborationSteps(run.ID)
		if err != nil {
			errs <- err
			return
		}
		planner, found := latestCompletedCollaborationStep(steps, "planner")
		if !found {
			errs <- errors.New("completed planner step not found")
			return
		}
		router, found := latestCompletedCollaborationStep(steps, "router")
		if !found {
			errs <- errors.New("completed router step not found; recover from the plan approval boundary")
			return
		}
		workerAgent, found := findAgentByID(restored.candidateAgents, router.AgentID)
		if !found {
			errs <- errors.New("frozen routed worker not found")
			return
		}
		run, err = r.store.UpdateRunStatus(run.ID, domain.RunRunning, "")
		if err != nil {
			errs <- err
			return
		}
		r.publishRunLifecycle(executionCtx, run, domain.EventRunResumed, map[string]any{"status": run.Status})
		events <- liveRunEvent(run)
		prepared := PreparedCollaborationRun{WorkerAgent: workerAgent, Run: run}
		workerStep, found := latestCompletedCollaborationStep(steps, "worker")
		workerOutput := ""
		if found {
			workerOutput = boundedWorkerHandoff(workerStep)
		} else {
			workerInput := fmt.Sprintf("User task:\n%s\n\nPlanner output:\n%s\n\nRouter-selected worker:\n%s (%s)", planner.Input, planner.Output, workerAgent.Name, workerAgent.ID)
			workerOutput, err = r.runWorkerStage(executionCtx, events, prepared, restored.catalog, workerInput, userInputRetrievalQuery(planner.Input))
			if err != nil {
				errs <- err
				return
			}
		}
		reviewStep, found := latestCompletedCollaborationStep(steps, "reviewer")
		review := ""
		if found {
			review = reviewStep.Output
		} else {
			reviewInput := fmt.Sprintf("User task:\n%s\n\nPlan:\n%s\n\nWorker result:\n%s", planner.Input, planner.Output, workerOutput)
			review, err = r.runCollaborationStep(executionCtx, events, prepared, "reviewer", "", reviewerPrompt(), reviewInput, userInputRetrievalQuery(planner.Input))
			if err != nil {
				errs <- err
				return
			}
		}
		finalStep, found := latestCompletedCollaborationStep(steps, "finalizer")
		final := ""
		if found {
			final = finalStep.Output
		} else {
			finalInput := fmt.Sprintf("User task:\n%s\n\nPlan:\n%s\n\nWorker result:\n%s\n\nReview:\n%s", planner.Input, planner.Output, workerOutput, review)
			final, err = r.runCollaborationStep(executionCtx, events, prepared, "finalizer", "", finalizerPrompt(), finalInput, userInputRetrievalQuery(planner.Input))
			if err != nil {
				errs <- err
				return
			}
		}
		r.emitFinalDeltas(executionCtx, run.ID, final, events)
	}()
	return events, errs
}
