package toolprogress

import (
	"context"
	"testing"
)

func TestGuardEscalatesRepeatedTypedFailureWithoutExecutingBlockedCalls(t *testing.T) {
	guard := New(DefaultConfig())
	call := testCall("args-a")
	failure := Outcome{ErrorCode: "execution_failed", ErrorCategory: "execution"}

	for count := 1; count <= 3; count++ {
		if before := guard.Before(call); before.Action != ActionAllow {
			t.Fatalf("attempt %d blocked early: %#v", count, before)
		}
		decision := guard.Observe(call, failure)
		want := ActionAllow
		if count >= 2 {
			want = ActionWarn
		}
		if decision.Action != want || decision.Count != count || !decision.Executed || decision.Rule != RuleRepeatedFailure {
			t.Fatalf("attempt %d decision=%#v", count, decision)
		}
	}
	blocked := guard.Before(call)
	if blocked.Action != ActionBlockCall || blocked.Count != 4 || blocked.Executed {
		t.Fatalf("expected fourth attempt to block: %#v", blocked)
	}
	halted := guard.Before(call)
	if halted.Action != ActionHaltTurn || halted.Count != 5 {
		t.Fatalf("expected fifth attempt to halt: %#v", halted)
	}
}

func TestGuardDoesNotConflateChangedArgumentsOrFailureCategories(t *testing.T) {
	guard := New(DefaultConfig())
	failure := Outcome{ErrorCode: "execution_failed", ErrorCategory: "execution"}
	guard.Observe(testCall("args-a"), failure)
	guard.Observe(testCall("args-a"), failure)

	changedArguments := testCall("args-b")
	if decision := guard.Before(changedArguments); decision.Action != ActionAllow {
		t.Fatalf("changed arguments were blocked: %#v", decision)
	}
	if decision := guard.Observe(changedArguments, failure); decision.Count != 1 || decision.Action != ActionAllow {
		t.Fatalf("changed arguments did not start a new pattern: %#v", decision)
	}

	changedFailure := Outcome{ErrorCode: "execution_timeout", ErrorCategory: "timeout"}
	if decision := guard.Observe(changedArguments, changedFailure); decision.Count != 1 || decision.Action != ActionAllow {
		t.Fatalf("changed failure category did not reset the pattern: %#v", decision)
	}
}

func TestGuardTracksReadOnlyResultsButNeverWriteResultContent(t *testing.T) {
	guard := New(DefaultConfig())
	call := testCall("args")
	read := Outcome{ReadOnly: true, EncodedResult: []byte(`{"value":1}`)}
	guard.Observe(call, read)
	if decision := guard.Observe(call, read); decision.Action != ActionWarn || decision.Rule != RuleRepeatedResult {
		t.Fatalf("unchanged read-only result was not warned: %#v", decision)
	}

	guard.Observe(call, Outcome{SuccessfulWrite: true, EncodedResult: []byte(`{"secret":"never-hash"}`)})
	if decision := guard.Before(call); decision.Action != ActionAllow {
		t.Fatalf("successful write did not reset history: %#v", decision)
	}
	if decision := guard.Observe(call, Outcome{EncodedResult: []byte(`{"value":1}`)}); decision.Trackable {
		t.Fatalf("non-read-only result became trackable: %#v", decision)
	}
}

func TestGuardDetectsAlternatingLoopAndRestorePreservesEscalation(t *testing.T) {
	guard := New(DefaultConfig())
	a, b := testCall("a"), testCall("b")
	outcome := Outcome{ErrorCode: "execution_failed", ErrorCategory: "execution"}
	for _, call := range []Call{a, b, a, b, a} {
		if decision := guard.Before(call); decision.Action == ActionBlockCall || decision.Action == ActionHaltTurn {
			t.Fatalf("alternating loop blocked before threshold: %#v", decision)
		}
		guard.Observe(call, outcome)
	}
	blocked := guard.Before(b)
	if blocked.Action != ActionBlockCall || blocked.Rule != RuleAlternatingLoop || blocked.Count != 4 {
		t.Fatalf("alternating loop not blocked: %#v", blocked)
	}

	restored := New(DefaultConfig())
	records := make([]Record, 0, 6)
	for _, call := range []Call{a, b, a, b, a} {
		decision := New(DefaultConfig()).Observe(call, outcome)
		decision.Rule = RuleAlternatingLoop
		decision.Count = 1
		records = append(records, Record{Decision: decision})
	}
	records = append(records, Record{Decision: blocked})
	restored.Restore(records)
	if decision := restored.Before(b); decision.Action != ActionHaltTurn {
		t.Fatalf("restored blocked attempt did not escalate: %#v", decision)
	}
}

func TestGuardConfigContextAndReset(t *testing.T) {
	config := NormalizeConfig(Config{Enabled: true})
	if !ValidateConfig(config) || ValidateConfig(Config{}) {
		t.Fatalf("unexpected config validation: %#v", config)
	}
	guard := New(config)
	ctx := WithGuard(context.Background(), guard)
	if FromContext(ctx) != guard || FromContext(nil) != nil || FromContext(context.Background()) != nil {
		t.Fatal("guard context round trip failed")
	}
	guard.Observe(testCall("args"), Outcome{ErrorCode: "failed", ErrorCategory: "execution"})
	guard.Reset()
	if decision := guard.Before(testCall("args")); decision.Action != ActionAllow {
		t.Fatalf("reset retained history: %#v", decision)
	}
	if New(DisabledConfig()).Observe(testCall("args"), Outcome{ErrorCode: "failed", ErrorCategory: "execution"}).Trackable {
		t.Fatal("disabled guard tracked an outcome")
	}
	var nilGuard *Guard
	if nilGuard.Config().Enabled || nilGuard.Before(testCall("args")).Action != ActionAllow ||
		nilGuard.Observe(testCall("args"), Outcome{}).Action != ActionAllow {
		t.Fatal("nil Guard did not fail open")
	}
	nilGuard.Restore(nil)
	nilGuard.Reset()
	if WithGuard(ctx, nil) != ctx {
		t.Fatal("nil Guard changed context")
	}
}

func TestGuardBoundsLiveAndRestoredHistory(t *testing.T) {
	config := DefaultConfig()
	config.HistoryMax = 4
	guard := New(config)
	records := make([]Record, 0, 6)
	for index := 0; index < 6; index++ {
		call := testCall(string(rune('a' + index)))
		decision := guard.Observe(call, Outcome{ErrorCode: "failed", ErrorCategory: "execution"})
		records = append(records, Record{Decision: decision})
	}
	restored := New(config)
	restored.Restore(append([]Record{{Decision: Decision{Version: "old", Executed: true}}}, records...))
	if len(guard.history) != 4 || len(restored.history) != 4 {
		t.Fatalf("history was not bounded: live=%d restored=%d", len(guard.history), len(restored.history))
	}
}

func testCall(argumentsHash string) Call {
	return Call{Source: "local", Tool: "lookup", DefinitionRevision: "revision-1", ArgumentsHash: argumentsHash}
}
