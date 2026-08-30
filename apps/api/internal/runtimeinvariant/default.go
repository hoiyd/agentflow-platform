package runtimeinvariant

import (
	"agentflow-platform/apps/api/internal/budget"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/invariant"
	"agentflow-platform/apps/api/internal/requestcapture"
	"agentflow-platform/apps/api/internal/verification"
)

func DefaultRegistry() *invariant.Registry {
	return invariant.NewRegistry(
		invariant.CheckFunc{CheckName: "event_protocol", Run: func(input invariant.Input) []domain.RuntimeInvariantFailure {
			return event.CheckRuntimeInvariants(input.Replay.Run, input.Replay.RunEvents)
		}},
		invariant.CheckFunc{CheckName: "model_request_reconstructability", Run: func(input invariant.Input) []domain.RuntimeInvariantFailure {
			return requestcapture.CheckRuntimeInvariants(input.Replay.Run, input.ModelRequests, input.Replay.RunEvents)
		}},
		invariant.CheckFunc{CheckName: "usage_ledger", Run: func(input invariant.Input) []domain.RuntimeInvariantFailure {
			return budget.CheckRuntimeInvariants(input.Replay.Run.ID, input.Replay.UsageLedger.Entries)
		}},
		invariant.CheckFunc{CheckName: "verification_evidence", Run: func(input invariant.Input) []domain.RuntimeInvariantFailure {
			return verification.CheckRuntimeInvariants(input.Replay.Run, input.Replay.VerificationEvidence)
		}},
	)
}
