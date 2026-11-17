package runtimeinvariant

import (
	"sort"

	"agentflow-platform/apps/api/internal/budget"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/inference/capture"
	"agentflow-platform/apps/api/internal/verification"
)

func Check(replay domain.RunReplay, requests []domain.ModelRequestRecord) []domain.RuntimeInvariantFailure {
	failures := []domain.RuntimeInvariantFailure{}
	failures = append(failures, event.CheckRuntimeInvariants(replay.Run, replay.RunEvents)...)
	failures = append(failures, capture.CheckRuntimeInvariants(replay.Run, requests, replay.RunEvents)...)
	failures = append(failures, budget.CheckRuntimeInvariants(replay.Run.ID, replay.UsageLedger.Entries)...)
	failures = append(failures, verification.CheckRuntimeInvariants(replay.Run, replay.VerificationEvidence)...)
	sort.SliceStable(failures, func(i, j int) bool {
		a, b := failures[i], failures[j]
		if a.Sequence != b.Sequence {
			return a.Sequence < b.Sequence
		}
		if a.Owner != b.Owner {
			return a.Owner < b.Owner
		}
		return a.Code < b.Code
	})
	return failures
}
