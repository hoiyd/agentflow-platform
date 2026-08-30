package runtimeinvariant

import (
	"testing"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/invariant"
)

func TestDefaultRegistryComposesPackageOwnedChecks(t *testing.T) {
	failures := DefaultRegistry().Evaluate(invariant.Input{Replay: domain.RunReplay{Run: domain.Run{ID: "run-1"}}})
	if len(failures) != 1 || failures[0].Owner != "requestcapture" || failures[0].Code != "model_request_snapshot_missing" {
		t.Fatalf("unexpected default diagnostics: %#v", failures)
	}
}
