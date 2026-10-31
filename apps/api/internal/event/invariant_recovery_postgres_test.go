package event_test

import (
	"encoding/json"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/recovery"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/testsupport/pgfixture"
)

// Preserve real failure provenance: neither path bypasses lifecycle facts with
// UpdateRunStatus. Reopening verifies JSON payload types and Replay boundaries.
func TestRecoverableLifecyclePostgresReplay(t *testing.T) {
	database := pgfixture.DatabaseURL(t)
	s, err := store.NewPostgresStore(database)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	agent, _, err := s.GetDefaultAgent()
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{"completion rejection", "cold startup repair"} {
		t.Run(source, func(t *testing.T) {
			conversation, err := s.CreateConversation("Recoverable lifecycle source ordering")
			if err != nil {
				t.Fatal(err)
			}
			run, err := s.CreateRunWithContract(agent.ID, conversation.ID, domain.RuntimeSnapshot{SchemaVersion: domain.CurrentRuntimeSnapshotVersion, Mode: "autonomous", RunBudget: &domain.RuntimeRunBudget{}}, nil)
			if err != nil {
				t.Fatal(err)
			}
			transition := func(from, to domain.RunStatus, kind domain.RunEventType) {
				t.Helper()
				result, err := s.TransitionRun(domain.RunTransition{RunID: run.ID, From: from, To: to, EventType: kind})
				if err != nil || !result.Applied {
					t.Fatalf("%s -> %s: applied=%t %v", from, to, result.Applied, err)
				}
			}
			transition(domain.RunQueued, domain.RunRunning, domain.EventRunStarted)
			if source == "cold startup repair" {
				count, err := recovery.MarkStaleRunningRuns(s, time.Nanosecond)
				if err != nil || count != 1 {
					t.Fatalf("real cold repair: count=%d %v", count, err)
				}
			} else {
				transition(domain.RunRunning, domain.RunFailedRecoverable, domain.EventRunFailed)
			}
			for attempt := 0; attempt < 2; attempt++ {
				transition(domain.RunFailedRecoverable, domain.RunRunning, domain.EventRunResumed)
				reopened, err := store.NewPostgresStore(database)
				if err != nil {
					t.Fatal(err)
				}
				defer reopened.Close()
				current, found, err := reopened.GetRun(run.ID)
				if err != nil || !found {
					t.Fatalf("missing resumed Run: %v", err)
				}
				items, err := reopened.ListRunEvents(run.ID)
				if err != nil {
					t.Fatal(err)
				}
				if failures := event.CheckRuntimeInvariants(current, items); len(failures) != 0 {
					t.Errorf("valid persisted Resume rejected: %+v", failures)
				}
				if attempt == 0 {
					transition(domain.RunRunning, domain.RunFailedRecoverable, domain.EventRunFailed)
				} else {
					transition(domain.RunRunning, domain.RunCompleted, domain.EventRunCompleted)
				}
			}
			replay, found, err := s.GetRunReplay(run.ID)
			if err != nil || !found || replay.Run.Status != domain.RunCompleted {
				t.Fatalf("Replay lost completed Run: found=%t %v", found, err)
			}
			if len(replay.Projection.InvariantFailures) != 0 {
				t.Errorf("Replay reports false lifecycle violation: %+v", replay.Projection.InvariantFailures)
			}
			if failures := event.CheckRuntimeInvariants(replay.Run, replay.RunEvents); len(failures) != 0 {
				t.Errorf("completed recovered lifecycle rejected: %+v", failures)
			}
			data, _ := json.Marshal(map[string]any{"schema": "ref01-recoverable-lifecycle-v1", "source": source, "run": replay.Run, "events": replay.RunEvents, "invariant_failures": replay.Projection.InvariantFailures, "limitations": []string{"real store transitions/repair and Replay, not a resumed model execution"}})
			t.Log("REF01_RECOVERY_EVIDENCE " + string(data))
		})
	}
}
