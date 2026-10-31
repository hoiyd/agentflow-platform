package store

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/redaction"
)

var ErrRunTransitionInvalid = errors.New("invalid run transition")

// PrepareRunTransition is shared by Postgres and fixtures; callers hold the Run
// lock until both the returned Run and Events have been committed.
func PrepareRunTransition(run domain.Run, request domain.RunTransition, cursor int64, now time.Time) (domain.RunTransitionResult, error) {
	result := domain.RunTransitionResult{Run: run}
	if request.RunID != run.ID || !validRunTransition(request) {
		return result, fmt.Errorf("%w: %s -> %s (%s)", ErrRunTransitionInvalid, request.From, request.To, request.EventType)
	}
	starting := request.EventType == domain.EventRunStarted
	if run.Status != request.From || (run.Status == request.To && !starting) || (starting && cursor != 0) {
		return result, nil
	}
	request.Error, _ = redaction.Text(strings.TrimSpace(request.Error))
	ApplyRunStatus(&result.Run, request.To, request.Error, now)
	payload := make(map[string]any, len(request.Payload)+2)
	for key, value := range request.Payload {
		payload[key] = value
	}
	payload["status"] = request.To
	if request.Error != "" {
		payload["error"] = request.Error
	}
	payload, _ = redaction.Map(payload)
	if cursor == 0 {
		created, err := PrepareRunEvent(domain.RunEvent{RunID: run.ID, ConversationID: run.ConversationID, Type: domain.EventRunCreated, Payload: map[string]any{"status": domain.RunQueued}}, cursor+1, now)
		if err != nil {
			return domain.RunTransitionResult{}, err
		}
		result.Events = append(result.Events, created)
		cursor++
	}
	event, err := PrepareRunEvent(domain.RunEvent{RunID: run.ID, ConversationID: run.ConversationID, Type: request.EventType, Payload: payload}, cursor+1, now)
	if err != nil {
		return domain.RunTransitionResult{}, err
	}
	result.Events = append(result.Events, event)
	result.Applied = true
	return result, nil
}

func validRunTransition(r domain.RunTransition) bool {
	switch r.EventType {
	case domain.EventRunStarted:
		return r.To == domain.RunRunning && (r.From == domain.RunQueued || r.From == domain.RunRunning)
	case domain.EventRunResumed:
		return r.To == domain.RunRunning && (r.From == domain.RunWaitingForUser || r.From == domain.RunFailedRecoverable)
	case domain.EventRunCancelRequested:
		return r.To == domain.RunCanceling && r.From == domain.RunRunning
	case domain.EventRunCanceled:
		return r.To == domain.RunCanceled && r.From != domain.RunCompleted && r.From != domain.RunFailed && knownRunStatus(r.From)
	case domain.EventRunCompleted:
		return r.To == domain.RunCompleted && completionSource(r.From)
	case domain.EventRunFailed:
		return (r.To == domain.RunFailed || r.To == domain.RunFailedRecoverable) && (completionSource(r.From) || r.From == domain.RunCanceling || r.From == domain.RunQueued) && r.From != domain.RunCompleted
	case domain.EventRunWaitingForUser:
		return r.To == domain.RunWaitingForUser && completionSource(r.From) && r.From != domain.RunCompleted
	default:
		return false
	}
}

func completionSource(status domain.RunStatus) bool {
	return status == domain.RunRunning || status == domain.RunWaitingForUser || status == domain.RunFailedRecoverable || status == domain.RunFailed || status == domain.RunCompleted
}

func knownRunStatus(status domain.RunStatus) bool {
	return completionSource(status) || status == domain.RunQueued || status == domain.RunCanceling || status == domain.RunCanceled
}

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
