package runtimeinvariant

import (
	"sort"

	"agentflow-platform/apps/api/internal/budget"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/requestcapture"
	"agentflow-platform/apps/api/internal/verification"
)

type Input struct {
	Replay        domain.RunReplay
	ModelRequests []domain.ModelRequestRecord
}

func Evaluate(input Input) []domain.RuntimeInvariantFailure {
	failures := event.CheckRuntimeInvariants(input.Replay.Run, input.Replay.RunEvents)
	failures = append(failures, requestcapture.CheckRuntimeInvariants(input.Replay.Run, input.ModelRequests, input.Replay.RunEvents)...)
	failures = append(failures, budget.CheckRuntimeInvariants(input.Replay.Run.ID, input.Replay.UsageLedger.Entries)...)
	failures = append(failures, verification.CheckRuntimeInvariants(input.Replay.Run, input.Replay.VerificationEvidence)...)
	sort.SliceStable(failures, func(i, j int) bool {
		if failures[i].Sequence != failures[j].Sequence {
			return failures[i].Sequence < failures[j].Sequence
		}
		if failures[i].Owner != failures[j].Owner {
			return failures[i].Owner < failures[j].Owner
		}
		return failures[i].Code < failures[j].Code
	})
	return failures
}
