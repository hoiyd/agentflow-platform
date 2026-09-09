package store

import (
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/domain"
)

func ApplyRunStatus(run *domain.Run, status domain.RunStatus, errorMessage string, now time.Time) {
	wasRunning := run.Status == domain.RunRunning
	willRun := status == domain.RunRunning
	if wasRunning && !willRun && run.ExecutionStartedAt != nil {
		run.ActiveRuntimeMS += max(int64(0), now.Sub(*run.ExecutionStartedAt).Milliseconds())
		run.ExecutionStartedAt = nil
	}
	if !wasRunning && willRun {
		run.ExecutionStartedAt = &now
	}
	run.Status = status
	run.Error = strings.TrimSpace(errorMessage)
	run.UpdatedAt = now
	if status == domain.RunRunning && run.StartedAt == nil {
		run.StartedAt = &now
	}
	if status == domain.RunRunning {
		run.HeartbeatAt = &now
		run.CompletedAt = nil
	}
	if status == domain.RunWaitingForUser {
		run.CompletedAt = nil
	}
	if status == domain.RunCompleted || status == domain.RunFailed || status == domain.RunFailedRecoverable || status == domain.RunCanceled {
		run.CompletedAt = &now
	}
}
