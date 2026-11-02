package agent

import (
	"context"
	"errors"

	"agentflow-platform/apps/api/internal/clarification"
	"agentflow-platform/apps/api/internal/domain"
)

// ResumeClarification is called only after application admission. The owner
// is authenticated; the model cannot supply or replace it.
func (r *Runtime) ResumeClarification(ctx context.Context, owner, workspace, runID, id string) (<-chan domain.RunEvent, <-chan error) {
	events := make(chan domain.RunEvent)
	errs := make(chan error, 1)
	go func() {
		defer close(events)
		defer close(errs)
		if err := r.resumeClarification(ctx, owner, workspace, runID, id, events); err != nil {
			errs <- err
		}
	}()
	return events, errs
}

func (r *Runtime) resumeClarification(ctx context.Context, owner, workspace, runID, id string, events chan<- domain.RunEvent) (err error) {
	plan, err := r.prepareClarificationResume(ctx, owner, workspace, runID, id)
	if err != nil {
		return err
	}
	if plan.kind == clarificationResumeCollaboration {
		resumed, failures := r.ResumeRecoverableCollaboration(ctx, runID)
		return forwardClarificationResume(resumed, failures, events)
	}
	committed, err := r.clarifications.ClaimClarification(ctx, owner, workspace, runID, id)
	if err != nil {
		return err
	}
	record := committed.Clarification
	// Only a successful claim owns Stage settlement; validation/delegation above
	// must not settle another execution's Stage. Pending is a new durable pause.
	defer func() {
		if err != nil && !errors.Is(err, clarification.Pending) && record.StageID != "" {
			err = errors.Join(err, r.failResumedClarification(ctx, runID, record.StageID, err))
		}
	}()
	saved, err := restoreClarificationCheckpoint(committed.Checkpoint, plan.run, record)
	if err != nil {
		return err
	}
	executionCtx, release := r.bindRunCancellation(ctx, runID)
	defer release()
	run, _, err := r.store.GetRun(runID)
	if err != nil {
		return err
	}
	if run.Status != domain.RunRunning {
		return context.Canceled
	}
	events <- liveRunEvent(run)
	if plan.kind == clarificationResumeReplay {
		r.emitFinalDeltas(executionCtx, runID, plan.output, events)
		return nil
	}
	if saved.HumanInput {
		return r.resumeClarificationHumanInput(ctx, executionCtx, run, record, plan.restored, release, events)
	}
	return r.resumeClarificationTool(ctx, executionCtx, run, record, saved, plan, release, events)
}

func forwardClarificationResume(resumed <-chan domain.RunEvent, failures <-chan error, events chan<- domain.RunEvent) error {
	for item := range resumed {
		events <- item
	}
	return <-failures
}
