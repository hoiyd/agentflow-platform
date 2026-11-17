package runtimeinvariant

import (
	"sort"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
)

func TestCheckComposesPackageOwnedChecks(t *testing.T) {
	failures := Check(domain.RunReplay{Run: domain.Run{ID: "run-1"}}, nil)
	if len(failures) != 1 || failures[0].Owner != "requestcapture" || failures[0].Code != "model_request_snapshot_missing" {
		t.Fatalf("unexpected default diagnostics: %#v", failures)
	}
}

// Invalid event order must still yield deterministic, sequence/owner/code order.
func TestCheckSortsDiagnostics(t *testing.T) {
	replay := domain.RunReplay{Run: domain.Run{ID: "run-1"}, RunEvents: []domain.RunEvent{{RunID: "run-1", Sequence: 3}, {RunID: "run-1", Sequence: 1}}}
	failures := Check(replay, nil)
	if len(failures) < 2 {
		t.Fatalf("fixture did not produce multiple diagnostics: %#v", failures)
	}
	if !sort.SliceIsSorted(failures, func(i, j int) bool {
		a, b := failures[i], failures[j]
		if a.Sequence != b.Sequence {
			return a.Sequence < b.Sequence
		}
		if a.Owner != b.Owner {
			return a.Owner < b.Owner
		}
		return a.Code < b.Code
	}) {
		t.Fatalf("diagnostics out of order: %#v", failures)
	}
}
