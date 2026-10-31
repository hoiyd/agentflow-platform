package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/failure"
	"agentflow-platform/apps/api/internal/store"
)

var ErrRunStateChanged = errors.New("run state changed before execution")

// Startup/resume must win the status comparison before any model or Tool runs.
func (r *Runtime) transitionRun(run domain.Run, status domain.RunStatus, event domain.RunEventType, message string, payload map[string]any) (domain.Run, error) {
	result, err := r.store.TransitionRun(domain.RunTransition{RunID: run.ID, From: run.Status, To: status, Error: message, EventType: event, Payload: payload})
	if err == nil && !result.Applied {
		err = fmt.Errorf("%w: %s is %s", ErrRunStateChanged, run.ID, result.Run.Status)
	}
	return result.Run, err
}

func (r *Runtime) loadLifecycleRun(id string) (domain.Run, error) {
	run, found, err := r.store.GetRun(strings.TrimSpace(id))
	if err == nil && !found {
		err = store.ErrNotFound("run")
	}
	return run, err
}

func (r *Runtime) CompleteRun(id string) (domain.Run, error) {
	result, err := r.settleRun(domain.RunTransition{RunID: id, To: domain.RunCompleted, EventType: domain.EventRunCompleted}, false, nil)
	r.afterRunCompletion(result, err)
	return result.Run, err
}

// Only the completion gate may reconsider a failed candidate after revalidation.
func (r *Runtime) CompleteVerifiedRun(id string) (domain.Run, error) {
	result, err := r.settleRun(domain.RunTransition{RunID: id, To: domain.RunCompleted, EventType: domain.EventRunCompleted, Payload: map[string]any{"source": "completion_gate"}}, true, nil)
	r.afterRunCompletion(result, err)
	return result.Run, err
}

func (r *Runtime) afterRunCompletion(result domain.RunTransitionResult, err error) {
	if err == nil && result.Applied && result.Run.Status == domain.RunCompleted {
		r.scheduleSoftContextCompaction(result.Run)
	}
}

func (r *Runtime) FailRun(id string, cause error) (domain.Run, error) {
	message := ""
	if cause != nil {
		message = cause.Error()
	}
	result, err := r.settleRun(domain.RunTransition{RunID: id, To: domain.RunFailed, EventType: domain.EventRunFailed, Error: message, Payload: failure.Merge(nil, cause)}, false, cause)
	return result.Run, err
}

func (r *Runtime) RejectRunCompletion(id string, status domain.RunStatus, reason string) (domain.Run, error) {
	kind := domain.EventRunFailed
	switch status {
	case domain.RunFailed, domain.RunFailedRecoverable:
	case domain.RunWaitingForUser:
		kind = domain.EventRunWaitingForUser
	default:
		return domain.Run{}, fmt.Errorf("invalid verification rejection status %q", status)
	}
	result, err := r.settleRun(domain.RunTransition{RunID: id, To: status, EventType: kind, Error: strings.TrimSpace(reason), Payload: map[string]any{"source": "completion_gate"}}, true, nil)
	return result.Run, err
}

func (r *Runtime) settleRun(request domain.RunTransition, verification bool, cause error) (domain.RunTransitionResult, error) {
	// A concurrent Stop can change running -> canceling while a worker finishes.
	// Re-evaluate the committed state, never replay the stale terminal decision.
	for range 4 {
		run, err := r.loadLifecycleRun(request.RunID)
		if err != nil {
			return domain.RunTransitionResult{}, err
		}
		if run.Status == domain.RunCompleted || run.Status == domain.RunCanceled || run.Status == request.To || (run.Status == domain.RunFailed && !verification) {
			r.forgetProgressGuard(run.ID)
			return domain.RunTransitionResult{Run: run}, nil
		}
		next := request
		next.From = run.Status
		if run.Status == domain.RunCanceling && (request.To != domain.RunFailed || verification || errors.Is(cause, context.Canceled)) {
			next.To, next.EventType, next.Error = domain.RunCanceled, domain.EventRunCanceled, "canceled by user"
		}
		result, err := r.store.TransitionRun(next)
		if err != nil {
			return result, err
		}
		if result.Applied {
			r.forgetProgressGuard(run.ID)
			return result, nil
		}
	}
	return domain.RunTransitionResult{}, ErrRunStateChanged
}

func (r *Runtime) CancelRun(id string) (domain.Run, error) {
	for range 4 {
		run, err := r.loadLifecycleRun(id)
		if err != nil {
			return run, err
		}
		switch run.Status {
		case domain.RunCompleted, domain.RunFailed:
			return run, nil
		case domain.RunCanceled:
			return run, r.closeCanceledClarification(run)
		case domain.RunCanceling:
			r.cancelActiveRun(run.ID)
			return run, nil
		}
		status, kind, reason := domain.RunCanceled, domain.EventRunCanceled, "canceled by user"
		if run.Status == domain.RunRunning {
			status, kind, reason = domain.RunCanceling, domain.EventRunCancelRequested, "cancel requested by user"
		}
		result, err := r.store.TransitionRun(domain.RunTransition{RunID: run.ID, From: run.Status, To: status, EventType: kind, Error: reason})
		if err != nil {
			return result.Run, err
		}
		if !result.Applied {
			continue
		}
		r.cancelActiveRun(run.ID)
		if status == domain.RunCanceled {
			r.forgetProgressGuard(run.ID)
			return result.Run, r.closeCanceledClarification(result.Run)
		}
		return result.Run, nil
	}
	return domain.Run{}, ErrRunStateChanged
}
