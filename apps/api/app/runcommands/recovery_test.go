package runcommands

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"agentflow-platform/apps/api/internal/agent"
	"agentflow-platform/apps/api/internal/checkpoint"
	"agentflow-platform/apps/api/internal/concurrency"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/testsupport/fixturestore"
)

// Failure inventory: wrong-mode dispatch, unanswered or unreadable questions,
// duplicate reservation, unsupported Single mutation, and text-shaped execution failures.
func TestResumeRoutesPersistedRecoveryState(t *testing.T) {
	for _, tc := range []struct {
		name, mode             string
		status                 domain.RunStatus
		question, input, route string
	}{
		{"single unsupported", agent.ChatModeSingle, domain.RunFailedRecoverable, "", "resume", ""},
		{"multi recovery", agent.ChatModeMultiAgent, domain.RunFailedRecoverable, "", "", "multi"},
		{"autonomous recovery", agent.ChatModeAutonomous, domain.RunFailedRecoverable, "", "", "autonomous"},
		{"legacy human input", agent.ChatModeAutonomous, domain.RunWaitingForUser, "", "answer", "human"},
		{"multi approval is not resume", agent.ChatModeMultiAgent, domain.RunWaitingForUser, "", "answer", ""},
		{"answered single", agent.ChatModeSingle, domain.RunWaitingForUser, "answered", "", "clarification"},
		{"applied single", agent.ChatModeSingle, domain.RunFailedRecoverable, "applied", "", "clarification"},
		{"applied multi", agent.ChatModeMultiAgent, domain.RunFailedRecoverable, "applied", "", "clarification"},
		{"applied autonomous", agent.ChatModeAutonomous, domain.RunFailedRecoverable, "applied", "", "clarification"},
		{"pending cannot bypass", agent.ChatModeAutonomous, domain.RunWaitingForUser, "pending", "answer", ""},
		{"expired cannot bypass", agent.ChatModeAutonomous, domain.RunWaitingForUser, "expired", "answer", ""},
		{"failed read cannot fall back", agent.ChatModeAutonomous, domain.RunFailedRecoverable, "read-error", "resume", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			storage := &recoveryStore{Store: fixturestore.New()}
			conv, err := storage.CreateConversation("Recovery fixture")
			if err != nil {
				t.Fatal(err)
			}
			run, err := storage.CreateRunWithContract("agent_planner", conv.ID, domain.RuntimeSnapshot{SchemaVersion: domain.CurrentRuntimeSnapshotVersion, Mode: tc.mode, RunBudget: &domain.RuntimeRunBudget{}}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = storage.UpdateRunStatus(run.ID, tc.status, ""); err != nil {
				t.Fatal(err)
			}
			before, _, _ := storage.GetRun(run.ID)
			if tc.question == "read-error" {
				storage.readErr = errors.New("clarification storage unavailable")
			} else if tc.question != "" {
				storage.questions = []domain.Clarification{{ID: "question-1", RunID: run.ID, Status: tc.question}}
			}
			runtime := &recoveryRuntime{}
			capacity := &recoveryCapacity{RunController: concurrency.NewRunController(concurrency.RunOptions{MaxConcurrent: 1})}
			commands := Commands{Store: storage, Runtime: runtime, Capacity: capacity, Clarifications: storage}
			err = commands.Resume(t.Context(), Scope{Workspace: domain.NewWorkspaceScope(run.WorkspaceID), Owner: "super"}, run.ID, tc.input, Observer{})
			if err == nil {
				t.Fatal("fixture recovery must reject or stop at runtime probe")
			}
			if runtime.called != tc.route {
				t.Fatalf("route=%q want %q error=%v", runtime.called, tc.route, err)
			}
			if tc.route == "" {
				after, _, _ := storage.GetRun(run.ID)
				if !reflect.DeepEqual(before, after) || runtime.failed != 0 {
					t.Fatalf("pre-execution rejection mutated Run: before=%+v after=%+v failures=%d", before, after, runtime.failed)
				}
			} else if capacity.reservations != 1 {
				t.Fatalf("reserved %d times", capacity.reservations)
			}
		})
	}
}

func TestFailurePoliciesPreserveTypedPreExecutionRejections(t *testing.T) {
	for _, policy := range []func(error) (Reason, bool){ResumeFailurePolicy, ContinuationFailurePolicy} {
		for _, err := range []error{&agent.PreExecutionRejection{Err: errors.New("missing checkpoint")}, agent.ErrRunStateChanged} {
			if reason, fail := policy(err); reason != Conflict || fail {
				t.Fatalf("typed rejection: reason=%s fail=%v error=%v", reason, fail, err)
			}
		}
	}
}

func TestResumeExecutionFailureKeepsConflictClassificationButSettlesRun(t *testing.T) {
	for _, err := range []error{checkpoint.ErrCheckpointStale, checkpoint.ErrNeedsReconciliation, agent.ErrRuntimeSnapshotUnavailable} {
		if reason, fail := ResumeFailurePolicy(err); reason != Conflict || !fail {
			t.Fatalf("execution failure lost classification or settlement: reason=%s fail=%v error=%v", reason, fail, err)
		}
	}
}

func TestFailurePoliciesDoNotMatchExecutionErrorText(t *testing.T) {
	for _, policy := range []func(error) (Reason, bool){ResumeFailurePolicy, ContinuationFailurePolicy} {
		for _, text := range []string{"run is not waiting for user input", "provider: not recoverable", "worker failed"} {
			if _, fail := policy(errors.New(text)); !fail {
				t.Fatalf("execution error suppressed by text: %s", text)
			}
		}
	}
}

type recoveryStore struct {
	*fixturestore.Store
	questions []domain.Clarification
	readErr   error
}

func (s *recoveryStore) ListClarifications(context.Context, string, string, string) ([]domain.Clarification, error) {
	return s.questions, s.readErr
}

type recoveryCapacity struct {
	*concurrency.RunController
	reservations int
}

func (c *recoveryCapacity) Reserve() (*concurrency.Reservation, error) {
	c.reservations++
	return c.RunController.Reserve()
}

type recoveryRuntime struct {
	Runtime
	called string
	failed int
}

func (r *recoveryRuntime) probe(route string) (<-chan domain.RunEvent, <-chan error) {
	r.called = route
	events := make(chan domain.RunEvent)
	close(events)
	errs := make(chan error, 1)
	errs <- errors.New("runtime probe")
	close(errs)
	return events, errs
}
func (r *recoveryRuntime) ResumeAutonomous(context.Context, string, string) (<-chan domain.RunEvent, <-chan error) {
	return r.probe("human")
}
func (r *recoveryRuntime) ResumeRecoverableAutonomous(context.Context, string, string) (<-chan domain.RunEvent, <-chan error) {
	return r.probe("autonomous")
}
func (r *recoveryRuntime) ResumeRecoverableCollaboration(context.Context, string) (<-chan domain.RunEvent, <-chan error) {
	return r.probe("multi")
}
func (r *recoveryRuntime) ResumeClarification(context.Context, string, string, string, string) (<-chan domain.RunEvent, <-chan error) {
	return r.probe("clarification")
}
func (r *recoveryRuntime) FailRun(string, error) (domain.Run, error) {
	r.failed++
	return domain.Run{Status: domain.RunFailed}, nil
}
