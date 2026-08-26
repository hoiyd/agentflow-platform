package invariant

import (
	"fmt"
	"reflect"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
)

func TestRegistryEvaluatesChecksAndSortsStableDiagnostics(t *testing.T) {
	input := Input{Replay: domain.RunReplay{Run: domain.Run{ID: "run-1"}}}
	registry := NewRegistry(
		nil,
		CheckFunc{CheckName: "later", Run: func(got Input) []domain.RuntimeInvariantFailure {
			if got.Replay.Run.ID != "run-1" {
				t.Fatalf("input was not forwarded: %#v", got)
			}
			return []domain.RuntimeInvariantFailure{
				{Code: "z", Owner: "verification", Sequence: 2},
				{Code: "b", Owner: "event", Sequence: 1},
			}
		}},
		CheckFunc{CheckName: "earlier", Run: func(Input) []domain.RuntimeInvariantFailure {
			return []domain.RuntimeInvariantFailure{{Code: "a", Owner: "event", Sequence: 1}}
		}},
	)

	failures := registry.Evaluate(input)
	want := []string{"event/a/1", "event/b/1", "verification/z/2"}
	got := make([]string, 0, len(failures))
	for _, failure := range failures {
		got = append(got, fmt.Sprintf("%s/%s/%d", failure.Owner, failure.Code, failure.Sequence))
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("diagnostic order=%v want %v", got, want)
	}
}

func TestRegistryAndCheckFuncHandleEmptyConfiguration(t *testing.T) {
	var registry *Registry
	if failures := registry.Evaluate(Input{}); len(failures) != 0 {
		t.Fatalf("nil registry returned failures: %#v", failures)
	}
	check := CheckFunc{CheckName: "disabled"}
	if check.Name() != "disabled" || check.Evaluate(Input{}) != nil {
		t.Fatalf("unexpected empty check behavior: name=%q failures=%#v", check.Name(), check.Evaluate(Input{}))
	}
	if failures := NewRegistry().Evaluate(Input{}); len(failures) != 0 {
		t.Fatalf("empty registry returned failures: %#v", failures)
	}
}
