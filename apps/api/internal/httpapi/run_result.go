package httpapi

import (
	"net/http"

	"agentflow-platform/apps/api/internal/verification"
)

// getRunResult reads only the Run and its Verification records, not full Replay.
func (h *Handler) getRunResult(w http.ResponseWriter, r *http.Request) {
	scoped := h.scopedStore(r)
	run, ok, err := scoped.GetRun(r.PathValue("id"))
	if err != nil {
		writeFailure(w, r, http.StatusInternalServerError, err)
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "run not found")
		return
	}
	if run.CompletionContract == nil || run.CompletionContract.Output == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"code": "structured_output_not_requested", "error": "Run has no structured output contract"})
		return
	}
	evidence, err := scoped.ListVerificationEvidence(run.ID)
	if err != nil {
		writeFailure(w, r, http.StatusInternalServerError, err)
		return
	}
	artifacts, err := scoped.ListVerificationArtifacts(run.ID)
	if err != nil {
		writeFailure(w, r, http.StatusInternalServerError, err)
		return
	}
	result := verification.StructuredResult(run, evidence, artifacts)
	status := http.StatusOK
	if len(result.Value) == 0 {
		status = http.StatusConflict
	}
	writeJSON(w, status, result)
}
