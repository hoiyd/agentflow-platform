package httpapi

import (
	"log"
	"net/http"
	"strings"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/invariant"
	"agentflow-platform/apps/api/internal/planning"
	"agentflow-platform/apps/api/internal/projection"
	"agentflow-platform/apps/api/internal/runtimeinvariant"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/verification"
)

func hideRunSnapshot(run *domain.Run) {
	if run.RuntimeSnapshot != nil {
		run.ExecutionConstraint = run.RuntimeSnapshot.ExecutionConstraint
	}
	run.RuntimeSnapshot = nil
}

// enrichRunProjection joins request-backed observations without extra Store reads.
func (h *Handler) enrichRunProjection(scoped store.WorkspaceStore, replay *domain.RunReplay) error {
	replay.Plan = planning.Read(replay.RunEvents)
	replay.StructuredResult = verification.StructuredResult(replay.Run, replay.VerificationEvidence, replay.VerificationArtifacts)
	records, err := scoped.ListModelRequestRecords(replay.Run.ID)
	if err != nil {
		return err
	}
	replay.Projection.SkillEvidence = projection.BuildSkillEvidence(*replay, records)
	failures := runtimeinvariant.DefaultRegistry().Evaluate(invariant.Input{
		Replay: *replay, ModelRequests: records,
	})
	replay.Projection.InvariantFailures = failures
	for _, failure := range failures {
		log.Printf("runtime_invariant_failure code=%s owner=%s run_id=%s event_id=%s sequence=%d", failure.Code, failure.Owner, failure.RunID, failure.EventID, failure.Sequence)
	}
	return nil
}

func (h *Handler) getRunProjection(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/runs/"), "/projection"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "run id is required")
		return
	}
	scoped := h.scopedStore(r)
	read, ok, err := h.loadRunProjection(scoped, id)
	if err != nil {
		writeFailure(w, r, http.StatusInternalServerError, err)
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "run not found")
		return
	}
	writeJSON(w, http.StatusOK, read.Snapshot)
}
