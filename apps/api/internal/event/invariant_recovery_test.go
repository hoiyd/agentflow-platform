package event

import (
	"testing"

	"agentflow-platform/apps/api/internal/domain"
)

// Recoverable failure closes one execution segment, not the Run's right to Resume.
func TestRuntimeInvariantRecoverableResumeSegments(t *testing.T) {
	for _, payloadStatus := range []any{string(domain.RunFailedRecoverable), domain.RunFailedRecoverable} {
		t.Run(stringStatusName(payloadStatus), func(t *testing.T) {
			failure := runtimeEvent(3, domain.EventRunFailed)
			failure.Payload = map[string]any{"status": payloadStatus}
			base := []domain.RunEvent{runtimeEvent(1, domain.EventRunCreated), runtimeEvent(2, domain.EventRunStarted), failure}
			for _, scenario := range []struct {
				name   string
				status domain.RunStatus
				tail   []domain.RunEvent
				want   string
			}{
				{"recoverable", domain.RunFailedRecoverable, nil, ""},
				{"resumed", domain.RunRunning, []domain.RunEvent{runtimeEvent(4, domain.EventRunResumed)}, ""},
				{"resumed then completed", domain.RunCompleted, []domain.RunEvent{runtimeEvent(4, domain.EventRunResumed), runtimeEvent(5, domain.EventRunCompleted)}, ""},
				{"no Resume", domain.RunCompleted, []domain.RunEvent{runtimeEvent(4, domain.EventRunCompleted)}, "run_terminal_duplicate"},
				{"waiting without Resume", domain.RunWaitingForUser, []domain.RunEvent{runtimeEvent(4, domain.EventRunWaitingForUser)}, "run_transition_orphan"},
				{"new Start is not Resume", domain.RunRunning, []domain.RunEvent{runtimeEvent(4, domain.EventRunStarted)}, "run_event_after_terminal"},
			} {
				t.Run(scenario.name, func(t *testing.T) {
					items := append(append([]domain.RunEvent{}, base...), scenario.tail...)
					failures := CheckRuntimeInvariants(domain.Run{ID: "run-1", Status: scenario.status}, items)
					if scenario.want == "" {
						if len(failures) != 0 {
							t.Fatalf("legal segment rejected: %+v", failures)
						}
					} else if len(failures) != 1 || failures[0].Code != scenario.want {
						t.Fatalf("failures=%+v want=%s", failures, scenario.want)
					}
				})
			}
			second := runtimeEvent(5, domain.EventRunFailed)
			second.Payload = map[string]any{"status": payloadStatus}
			items := append(base, runtimeEvent(4, domain.EventRunResumed), second, runtimeEvent(6, domain.EventRunResumed), runtimeEvent(7, domain.EventRunCompleted))
			if failures := CheckRuntimeInvariants(domain.Run{ID: "run-1", Status: domain.RunCompleted}, items); len(failures) != 0 {
				t.Fatalf("multiple legitimate segments rejected: %+v", failures)
			}
		})
	}
}

func TestRuntimeInvariantFinalTerminalsCannotResume(t *testing.T) {
	for _, terminal := range []domain.RunEventType{domain.EventRunFailed, domain.EventRunCompleted, domain.EventRunCanceled} {
		t.Run(string(terminal), func(t *testing.T) {
			item := runtimeEvent(3, terminal)
			item.Payload = map[string]any{"status": "failed"}
			items := []domain.RunEvent{runtimeEvent(1, domain.EventRunCreated), runtimeEvent(2, domain.EventRunStarted), item, runtimeEvent(4, domain.EventRunResumed)}
			failures := CheckRuntimeInvariants(domain.Run{ID: "run-1", Status: domain.RunRunning}, items)
			if len(failures) != 1 || failures[0].Code != "run_transition_orphan" {
				t.Fatalf("terminal resumed: %+v", failures)
			}
		})
	}
}

func TestRuntimeInvariantCancellationBeforeWorkerStart(t *testing.T) {
	items := []domain.RunEvent{runtimeEvent(1, domain.EventRunCreated), runtimeEvent(2, domain.EventRunCancelRequested), runtimeEvent(3, domain.EventRunCanceled)}
	if failures := CheckRuntimeInvariants(domain.Run{ID: "run-1", Status: domain.RunCanceled}, items); len(failures) != 0 {
		t.Fatalf("pre-worker cancellation needs no invented Start: %+v", failures)
	}
}

func stringStatusName(status any) string {
	if _, ok := status.(string); ok {
		return "persisted string"
	}
	return "typed status"
}
