package agent

import (
	"encoding/json"

	"agentflow-platform/apps/api/internal/approval"
	"agentflow-platform/apps/api/internal/domain"
)

// Recover a committed Turn/Stage before another execution claim. A candidate
// lost before application completion must not regenerate model output or writes.
func (r *Runtime) completedToolApproval(run domain.Run, record domain.ToolApproval) (string, bool, error) {
	if run.Status != domain.RunFailedRecoverable {
		return "", false, nil
	}
	if record.StageID != "" {
		steps, err := r.store.ListCollaborationSteps(run.ID)
		if err != nil {
			return "", false, err
		}
		for _, step := range steps {
			if step.ID == record.StageID && step.Status == domain.CollaborationStepCompleted {
				return step.Output, true, nil
			}
		}
		return "", false, nil
	}
	items, err := r.store.ListRunEvents(run.ID)
	if err != nil {
		return "", false, err
	}
	claimed := false
	var output string
	completed := false
	for _, item := range items {
		if item.Type == domain.EventRunResumed && item.Payload["kind"] == "tool_approval" {
			data, _ := json.Marshal(item.Payload["approval"])
			var current domain.ToolApproval
			if json.Unmarshal(data, &current) != nil {
				return "", false, approval.ErrConflict
			}
			if current.ID == record.ID {
				claimed = true
				completed = false
			}
		}
		if claimed && item.Type == domain.EventTurnCompleted && item.TurnID == record.TurnID && item.Payload["stop_reason"] == "completed" {
			output, _ = item.Payload["output"].(string)
			completed = true
		}
	}
	return output, completed, nil
}
