package agent

import (
	"agentflow-platform/apps/api/internal/domain"
	"context"
	"errors"
)

// Pausing closed the Turn atomically but deliberately left its Stage open.
// Once execution is absent, Stop must close that suspended Stage as well.
func (r *Runtime) closeCanceledClarification(run domain.Run) error {
	if r.clarifications == nil && r.toolApprovals == nil {
		return nil
	}
	if _, active := r.activeRunCancels.Load(run.ID); active {
		return nil
	}
	items, err := r.store.ListRunEvents(run.ID)
	if err != nil {
		return err
	}
	waiting := map[string]bool{}
	for _, item := range items {
		if item.Type == domain.EventRunWaitingForUser && (item.Payload["kind"] == "clarification" || item.Payload["kind"] == "tool_approval") && item.StageID != "" {
			waiting[item.StageID] = true
		}
	}
	if len(waiting) == 0 {
		return nil
	}
	steps, err := r.store.ListCollaborationSteps(run.ID)
	if err != nil {
		return err
	}
	for _, step := range steps {
		if !waiting[step.ID] || step.Status != domain.CollaborationStepRunning {
			continue
		}
		if err = r.persistFailedStage(context.Background(), step, errors.New("waiting execution canceled by user"), nil); err != nil {
			return err
		}
	}
	return nil
}
