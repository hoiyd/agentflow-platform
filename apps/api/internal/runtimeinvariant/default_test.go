package runtimeinvariant

import (
	"testing"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/invariant"
)

func TestNormalizeMode(t *testing.T) {
	for _, test := range []struct {
		value string
		want  Mode
	}{
		{value: "fail", want: ModeFail},
		{value: " FAIL ", want: ModeFail},
		{value: "report", want: ModeReport},
		{value: "unknown", want: ModeReport},
	} {
		if got := NormalizeMode(test.value); got != test.want {
			t.Fatalf("NormalizeMode(%q)=%q want %q", test.value, got, test.want)
		}
	}
}

func TestFailureErrorReportsViolationCount(t *testing.T) {
	err := (&FailureError{Failures: []domain.RuntimeInvariantFailure{{}, {}}}).Error()
	if err != "runtime invariant check failed with 2 violation(s)" {
		t.Fatalf("unexpected error: %q", err)
	}
}

func TestDefaultRegistryComposesPackageOwnedChecks(t *testing.T) {
	failures := DefaultRegistry().Evaluate(invariant.Input{Replay: domain.RunReplay{Run: domain.Run{ID: "run-1"}}})
	if len(failures) != 1 || failures[0].Owner != "requestcapture" || failures[0].Code != "model_request_snapshot_missing" {
		t.Fatalf("unexpected default diagnostics: %#v", failures)
	}
}
