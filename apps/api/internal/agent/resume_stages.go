package agent

import (
	"context"

	"agentflow-platform/apps/api/internal/domain"
)

func (r *Runtime) failResumedStage(ctx context.Context, runID, stageID string, cause error) error {
	steps, err := r.store.ListCollaborationSteps(runID)
	if err != nil {
		return err
	}
	for _, step := range steps {
		if step.ID != stageID || step.Status != domain.CollaborationStepRunning {
			continue
		}
		return r.persistFailedStage(ctx, step, cause, nil)
	}
	return nil
}

func (r *Runtime) resumeAutonomousAfterTurn(ctx context.Context, run domain.Run, step domain.CollaborationStep, restored restoredRuntime, release func(), events chan<- domain.RunEvent) error {
	steps, err := r.store.ListCollaborationSteps(run.ID)
	if err != nil {
		return err
	}
	state := rebuildRecoverableAutonomousState(steps, domain.CollaborationStep{})
	state.NextIter = step.Iteration
	state.OutputChars = 0
	for _, s := range steps {
		if s.Iteration < step.Iteration && s.Status == domain.CollaborationStepCompleted {
			state.OutputChars += len(s.Output)
		}
	}
	prepared := PreparedCollaborationRun{WorkerAgent: restored.agent, Run: run}
	release()
	loopCtx := context.WithValue(ctx, resumedIterationKey{}, step.Iteration)
	resumed, failures := r.runAutonomousFromState(loopCtx, prepared, firstAutonomousTask(steps), state)
	return forwardResumedEvents(resumed, failures, events)
}
