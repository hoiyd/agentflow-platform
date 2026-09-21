package agent

import "agentflow-platform/apps/api/internal/testsupport/fixturestore"

import (
	"testing"

	"agentflow-platform/apps/api/internal/domain"

	"agentflow-platform/apps/api/internal/tool/progress"
)

func TestProgressGuardForRunRestoresTerminalToolHistory(t *testing.T) {
	fixtureStore := fixturestore.New()

	conversation, _ := fixtureStore.CreateConversation("progress restore")
	snapshot := testRuntimeSnapshot()
	run, err := fixtureStore.CreateRunWithContract("agent_planner", conversation.ID, snapshot, nil)
	if err != nil {
		t.Fatal(err)
	}
	call := progress.Call{Source: "local", Tool: "reader", DefinitionRevision: "revision", ArgumentsHash: "arguments"}
	seed := progress.New(snapshot.ToolProgressGuard)
	for count := 1; count <= 3; count++ {
		decision := seed.Observe(call, progress.Outcome{ErrorCode: "execution_failed", ErrorCategory: "execution"})
		_, err = fixtureStore.CreateRunEvent(domain.RunEvent{
			Type: domain.EventToolFailed, SchemaVersion: domain.CurrentRunEventSchemaVersion,
			RunID: run.ID, Payload: progressPayload(decision),
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	runtime := NewRuntime(RuntimeOptions{Store: fixtureStore, ModelClient: newLocalFallbackOpenAIClientForTest()})
	guard, err := runtime.progressGuardForRun(run.ID, run.RuntimeSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	if decision := guard.Before(call); decision.Action != progress.ActionBlockCall || decision.Count != 4 {
		t.Fatalf("restored decision=%#v", decision)
	}
	if runtimeGuard, err := runtime.progressGuardForRun(run.ID, run.RuntimeSnapshot); err != nil || runtimeGuard != guard {
		t.Fatalf("Run guard was not reused: guard=%p cached=%p err=%v", guard, runtimeGuard, err)
	}
	if err := runtime.resetProgressGuard(run); err != nil {
		t.Fatal(err)
	}
	if decision := guard.Before(call); decision.Action != progress.ActionAllow {
		t.Fatalf("human-input reset retained history: %#v", decision)
	}
	runtime.forgetProgressGuard(run.ID)
	if runtime.toolProgressGuards[run.ID] != nil {
		t.Fatal("terminal Run retained its in-memory Guard")
	}
}

func TestProgressGuardForHistoricalSnapshotIsDisabled(t *testing.T) {
	fixtureStore := fixturestore.New()

	snapshot := testRuntimeSnapshot()
	snapshot.SchemaVersion = domain.ToolSecurityRuntimeSnapshotVersion
	snapshot.ToolProgressGuard = progress.Config{}
	runtime := NewRuntime(RuntimeOptions{Store: fixtureStore, ModelClient: newLocalFallbackOpenAIClientForTest()})
	guard, err := runtime.progressGuardForRun("historical-run", &snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if guard.Config().Enabled {
		t.Fatalf("historical Run unexpectedly enabled guard: %#v", guard.Config())
	}
}

func TestProgressDecisionFromPayloadRejectsIncompleteAndDecodesJSONNumbers(t *testing.T) {
	if _, ok := progressDecisionFromPayload(map[string]any{}); ok {
		t.Fatal("incomplete payload was accepted")
	}
	decision, ok := progressDecisionFromPayload(map[string]any{
		"progress_guard_version":   progress.CurrentVersion,
		"progress_guard_action":    string(progress.ActionWarn),
		"progress_guard_rule":      string(progress.RuleRepeatedResult),
		"progress_guard_count":     float64(2),
		"progress_guard_trackable": true,
		"progress_guard_executed":  true,
	})
	if !ok || decision.Count != 2 || !decision.Trackable || !decision.Executed {
		t.Fatalf("decoded decision=%#v ok=%t", decision, ok)
	}
	if intValue(int64(3)) != 3 || intValue("bad") != 0 || boolValue("bad") {
		t.Fatal("payload scalar conversion mismatch")
	}
}

func progressPayload(decision progress.Decision) map[string]any {
	return map[string]any{
		"progress_guard_version":   decision.Version,
		"progress_guard_rule":      string(decision.Rule),
		"progress_guard_action":    string(decision.Action),
		"progress_guard_count":     decision.Count,
		"progress_guard_reason":    decision.Reason,
		"progress_guard_signature": decision.SignatureHash,
		"progress_guard_outcome":   decision.OutcomeFingerprint,
		"progress_guard_trackable": decision.Trackable,
		"progress_guard_executed":  decision.Executed,
	}
}
