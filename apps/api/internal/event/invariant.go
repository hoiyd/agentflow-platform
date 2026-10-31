package event

import (
	"errors"
	"fmt"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/eventcatalog"
)

type protocolViolation struct {
	failure domain.RuntimeInvariantFailure
}

func (e *protocolViolation) Error() string { return e.failure.Message }

func newProtocolViolation(code string, item domain.RunEvent, message string) error {
	return &protocolViolation{failure: domain.RuntimeInvariantFailure{
		Code: code, Owner: "event", RunID: item.RunID, EventID: item.ID,
		Sequence: item.Sequence, Message: message,
	}}
}

// CheckRuntimeInvariants reports event protocol failures without making Replay
// unavailable. Execution and recovery paths may still use ValidateLifecycle
// when they require a hard gate.
func CheckRuntimeInvariants(run domain.Run, events []domain.RunEvent) []domain.RuntimeInvariantFailure {
	state, err := scanLifecycle(events)
	if err != nil {
		var violation *protocolViolation
		if errors.As(err, &violation) {
			return []domain.RuntimeInvariantFailure{violation.failure}
		}
		return []domain.RuntimeInvariantFailure{{Code: "event_protocol_invalid", Owner: "event", Message: err.Error()}}
	}
	if failure := checkRunLifecycle(run, events); failure != nil {
		return []domain.RuntimeInvariantFailure{*failure}
	}
	if run.Status == domain.RunRunning || run.Status == domain.RunQueued || run.Status == domain.RunWaitingForUser || run.Status == domain.RunCanceling {
		return []domain.RuntimeInvariantFailure{}
	}
	openScopes := []struct {
		code  string
		items map[string]domain.RunEvent
	}{
		{code: "tool_terminal_missing", items: state.tools},
		{code: "model_terminal_missing", items: state.models},
		{code: "turn_terminal_missing", items: state.turns},
		{code: "stage_terminal_missing", items: state.stages},
	}
	for _, scope := range openScopes {
		if item, ok := firstOpen(scope.items); ok {
			return []domain.RuntimeInvariantFailure{{
				Code: scope.code, Owner: "event", RunID: item.RunID, EventID: item.ID,
				Sequence: item.Sequence, Message: fmt.Sprintf("%s opened at sequence %d has no terminal event", scope.code, item.Sequence),
			}}
		}
	}
	return []domain.RuntimeInvariantFailure{}
}

func checkRunLifecycle(run domain.Run, events []domain.RunEvent) *domain.RuntimeInvariantFailure {
	opened := false
	terminal := false
	recoverable := false
	terminalIndex := -1
	lastVerificationRequest := -1
	for index, item := range events {
		if item.Type == domain.EventVerificationRequested {
			lastVerificationRequest = index
		}
	}
	seenLifecycle := false
	var last domain.RunEvent
	for index, item := range events {
		switch item.Type {
		case domain.EventRunCreated, domain.EventRunStarted:
			seenLifecycle = true
			if terminal {
				failure := lifecycleFailure("run_event_after_terminal", item, "run lifecycle restarted after a terminal event")
				return &failure
			}
			opened = true
		case domain.EventRunResumed:
			seenLifecycle = true
			if !opened || terminal && !recoverable {
				failure := lifecycleFailure("run_transition_orphan", item, "run transition has no active lifecycle")
				return &failure
			}
			terminal, recoverable = false, false
		case domain.EventRunWaitingForUser, domain.EventRunCancelRequested, domain.EventRunRevisionRequested:
			seenLifecycle = true
			if !opened || terminal && !(events[terminalIndex].Type == domain.EventRunFailed && verificationBoundary(run, item, events[terminalIndex+1:index], index > lastVerificationRequest)) {
				failure := lifecycleFailure("run_transition_orphan", item, "run transition has no active lifecycle")
				return &failure
			}
			if terminal && item.Type == domain.EventRunWaitingForUser {
				terminal, recoverable = false, false
			}
		case domain.EventRunCompleted, domain.EventRunFailed, domain.EventRunCanceled:
			seenLifecycle = true
			if !opened {
				failure := lifecycleFailure("run_terminal_orphan", item, "run terminal event has no start")
				return &failure
			}
			if terminal && !(events[terminalIndex].Type == domain.EventRunFailed && verificationBoundary(run, item, events[terminalIndex+1:index], index > lastVerificationRequest)) {
				failure := lifecycleFailure("run_terminal_duplicate", item, "run has more than one terminal event")
				return &failure
			}
			terminal = true
			terminalIndex = index
			recoverable = item.Type == domain.EventRunFailed && (item.Payload["status"] == domain.RunFailedRecoverable || item.Payload["status"] == string(domain.RunFailedRecoverable))
		}
		last = item
	}
	finished := run.Status == domain.RunCompleted || run.Status == domain.RunFailed || run.Status == domain.RunCanceled || run.Status == domain.RunFailedRecoverable
	if seenLifecycle && finished && !terminal {
		failure := lifecycleFailure("run_terminal_missing", last, "finished run has no terminal event")
		failure.RunID = run.ID
		return &failure
	}
	if terminal && !finished {
		failure := lifecycleFailure("run_status_terminal_mismatch", last, "terminal event does not match an active Run status")
		failure.RunID = run.ID
		return &failure
	}
	return nil
}

func lifecycleFailure(code string, item domain.RunEvent, message string) domain.RuntimeInvariantFailure {
	return domain.RuntimeInvariantFailure{
		Code: code, Owner: "event", RunID: item.RunID, EventID: item.ID,
		Sequence: item.Sequence, Message: message,
	}
}

func validateRegisteredEvent(item domain.RunEvent) error {
	definition, ok := eventcatalog.DefinitionFor(item.Type)
	if !ok {
		return newProtocolViolation("event_type_unregistered", item, fmt.Sprintf("run event type %q is not registered", item.Type))
	}
	if item.SchemaVersion != definition.SchemaVersion {
		return newProtocolViolation("event_schema_unsupported", item,
			fmt.Sprintf("event %d has unsupported schema version %d", item.Sequence, item.SchemaVersion))
	}
	return nil
}
