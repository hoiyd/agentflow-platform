package runtimeinvariant

import (
	"fmt"
	"strings"

	"agentflow-platform/apps/api/internal/budget"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/invariant"
	"agentflow-platform/apps/api/internal/requestcapture"
	"agentflow-platform/apps/api/internal/verification"
)

type Mode string

const (
	ModeReport Mode = "report"
	ModeFail   Mode = "fail"
)

func NormalizeMode(value string) Mode {
	if strings.EqualFold(strings.TrimSpace(value), string(ModeFail)) {
		return ModeFail
	}
	return ModeReport
}

type FailureError struct {
	Failures []domain.RuntimeInvariantFailure
}

func (e *FailureError) Error() string {
	return fmt.Sprintf("runtime invariant check failed with %d violation(s)", len(e.Failures))
}

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
