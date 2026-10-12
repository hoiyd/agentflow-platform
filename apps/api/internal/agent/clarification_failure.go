package agent

import (
	"agentflow-platform/apps/api/internal/domain"
	"context"
)

func (r *Runtime) failResumedClarification(ctx context.Context, runID, stageID string, cause error) error {
	steps, err := r.store.ListCollaborationSteps(runID)
	if err != nil {
		return err
	}
	for _, step := range steps {
		if step.ID != stageID || step.Status != domain.CollaborationStepRunning {
			continue
		}
		failed, err := r.store.UpdateCollaborationStep(step.ID, domain.CollaborationStepFailed, step.Output, cause.Error())
		if err != nil {
			return err
		}
		kind := domain.EventStageFailed
		if run, found, err := r.store.GetRun(runID); err == nil && found && (run.Status == domain.RunCanceled || run.Status == domain.RunCanceling) {
			kind = domain.EventStageCanceled
		}
		if err = r.publishStage(context.WithoutCancel(ctx), failed, kind); err != nil {
			return err
		}
	}
	return nil
}
