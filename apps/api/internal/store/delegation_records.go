package store

import (
	"errors"

	"strings"

	"agentflow-platform/apps/api/internal/domain"
)

func ValidateChildRunRequest(request domain.ChildRunRequest) error {
	d := request.Delegation
	snapshot := request.RuntimeSnapshot
	frozen := snapshot.Delegation
	if strings.TrimSpace(d.ID) == "" || strings.TrimSpace(d.ParentRunID) == "" || strings.TrimSpace(d.ParentTurnID) == "" || strings.TrimSpace(d.AgentID) == "" || strings.TrimSpace(d.Task) == "" {
		return errors.New("delegation id, parent, agent, and task are required")
	}
	if d.Depth != 1 || d.TimeoutMS <= 0 || frozen == nil || frozen.DelegationID != d.ID || frozen.ParentRunID != d.ParentRunID || frozen.ParentTurnID != d.ParentTurnID || frozen.Depth != d.Depth || frozen.TimeoutMS != d.TimeoutMS || !frozen.IsolatedContext {
		return errors.New("invalid child run delegation snapshot")
	}
	if snapshot.SchemaVersion != domain.CurrentRuntimeSnapshotVersion || snapshot.Mode != "single" || snapshot.RunBudget == nil || snapshot.Agent.ID != d.AgentID {
		return errors.New("runtime snapshot is required")
	}
	return nil
}

func ValidateDelegationResult(result domain.DelegationResult) error {
	switch result.Status {
	case domain.DelegationCreated, domain.DelegationRunning,
		domain.DelegationCompleted, domain.DelegationFailed, domain.DelegationCanceled:
		if result.BlockReason != "" {
			return errors.New("delegation block reason requires blocked status")
		}
		return nil
	case domain.DelegationBlocked:
		if result.BlockReason != domain.DelegationBlockReasonChildRecoveryRequired {
			return errors.New("delegation block reason is invalid")
		}
		return nil
	default:
		return errors.New("delegation status is invalid")
	}
}
