package httpapi

import (
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/projection"
	"agentflow-platform/apps/api/internal/store"
)

type runProjectionRead struct {
	Snapshot domain.RunProjectionSnapshot
	Events   []domain.RunEvent
}

// Keep this dependency list aligned with runtimeinvariant.DefaultRegistry and
// Skill evidence. Recovery does not need messages, artifacts or checkpoints.
func (h *Handler) loadRunProjection(scoped store.WorkspaceStore, id string) (runProjectionRead, bool, error) {
	// ListRunEvents already authorizes the Run. Read its mutable status after
	// the event watermark, or a stopping event can be acknowledged with an old
	// status and never delivered again to the newly registered subscriber.
	events, err := scoped.ListRunEvents(id)
	if store.IsNotFound(err) {
		return runProjectionRead{}, false, nil
	}
	if err != nil {
		return runProjectionRead{}, false, err
	}
	run, ok, err := scoped.GetRun(id)
	if err != nil || !ok {
		return runProjectionRead{}, ok, err
	}
	ledger, ok, err := scoped.GetRunUsageLedger(id)
	if err != nil {
		return runProjectionRead{}, false, err
	}
	if !ok {
		return runProjectionRead{}, false, store.ErrNotFound("run")
	}
	evidence, err := scoped.ListVerificationEvidence(id)
	if err != nil {
		return runProjectionRead{}, false, err
	}
	// Existing projectors/checks consume only these fields. Full Replay uses the
	// same projectors, so there is no second read-model implementation.
	inputs := domain.RunReplay{Run: run, RuntimeSnapshot: run.RuntimeSnapshot, RunEvents: events, UsageLedger: ledger, VerificationEvidence: evidence,
		Projection: projection.BuildSnapshot(run, events, ledger, evidence)}
	if err := h.enrichRunProjection(scoped, &inputs); err != nil {
		return runProjectionRead{}, false, err
	}
	return runProjectionRead{Snapshot: inputs.Projection, Events: events}, true, nil
}
