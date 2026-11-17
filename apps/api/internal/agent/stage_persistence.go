package agent

import (
	"context"
	"errors"
	"fmt"

	"agentflow-platform/apps/api/internal/agent/turn"
	"agentflow-platform/apps/api/internal/domain"
)

func (r *Runtime) completeStage(ctx context.Context, stageID, output string, events chan<- domain.RunEvent) (domain.CollaborationStep, error) {
	step, err := r.store.UpdateCollaborationStep(stageID, domain.CollaborationStepCompleted, output, "")
	if err != nil {
		return domain.CollaborationStep{}, fmt.Errorf("complete stage: %w", err)
	}
	if err := r.publishStage(ctx, step, domain.EventStageCompleted); err != nil {
		return step, fmt.Errorf("persist completed stage checkpoint: %w", err)
	}
	emitCollaborationEvent(events, liveStageEvent(step))
	return step, nil
}

func (r *Runtime) persistFailedStage(ctx context.Context, step domain.CollaborationStep, cause error, events chan<- domain.RunEvent) error {
	if turn.IsWaiting(cause) {
		return nil
	}
	kind := domain.EventStageFailed
	run, found, err := r.store.GetRun(step.RunID)
	if err != nil {
		return fmt.Errorf("read failed stage run: %w", err)
	}
	if found && (run.Status == domain.RunCanceled || run.Status == domain.RunCanceling) {
		kind = domain.EventStageCanceled
	}
	failed, err := r.store.UpdateCollaborationStep(step.ID, domain.CollaborationStepFailed, step.Output, cause.Error())
	if err != nil {
		return fmt.Errorf("fail stage: %w", err)
	}
	// Cancellation must not prevent the terminal checkpoint from being recorded.
	if err := r.publishStage(context.WithoutCancel(ctx), failed, kind); err != nil {
		return fmt.Errorf("persist failed stage checkpoint: %w", err)
	}
	live := liveStageEvent(failed)
	live.Type = kind
	emitCollaborationEvent(events, live)
	return nil
}

func (r *Runtime) failCollaborationStage(ctx context.Context, events chan<- domain.RunEvent, step domain.CollaborationStep, cause error) error {
	if err := r.persistFailedStage(ctx, step, cause, events); err != nil {
		return errors.Join(cause, err)
	}
	return cause
}
