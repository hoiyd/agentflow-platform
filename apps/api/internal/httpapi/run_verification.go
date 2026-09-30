package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"agentflow-platform/apps/api/app/runcompletion"
)

func (h *Handler) verifyRun(w http.ResponseWriter, r *http.Request) {
	runID := strings.TrimSpace(r.PathValue("id"))
	if runID == "" {
		writeError(w, http.StatusBadRequest, "run id is required")
		return
	}
	scoped := h.scopedStore(r)
	run, ok, err := scoped.GetRun(runID)
	if err != nil {
		writeFailure(w, r, http.StatusInternalServerError, err)
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "run not found")
		return
	}
	result, err := runcompletion.Reverify(r.Context(), scoped, h.completionDependencies(), run)
	if err != nil {
		if errors.Is(err, runcompletion.ErrNotRequired) || errors.Is(err, runcompletion.ErrTerminal) || errors.Is(err, runcompletion.ErrNoCandidate) {
			writeError(w, http.StatusConflict, err.Error())
		} else {
			writeFailure(w, r, http.StatusInternalServerError, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"run": result.Run, "decision": result.Decision})
}
