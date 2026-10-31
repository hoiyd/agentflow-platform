package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"agentflow-platform/apps/api/app/runcommands"
	"agentflow-platform/apps/api/app/runcompletion"
)

func (h *Handler) verifyRun(w http.ResponseWriter, r *http.Request) {
	runID := strings.TrimSpace(r.PathValue("id"))
	if runID == "" {
		writeError(w, http.StatusBadRequest, "run id is required")
		return
	}
	result, err := h.runCommands().Reverify(r.Context(), commandScope(r, workspaceIDFromRequest(r)), runID)
	if err != nil {
		if errors.Is(err, runcompletion.ErrNotRequired) || errors.Is(err, runcompletion.ErrTerminal) || errors.Is(err, runcompletion.ErrNoCandidate) {
			writeError(w, http.StatusConflict, err.Error())
		} else {
			status := http.StatusInternalServerError
			var commandErr *runcommands.Error
			if errors.As(err, &commandErr) {
				status = commandStatus(commandErr.Reason)
				if commandErr.Reason == runcommands.Overload || commandErr.Reason == runcommands.QueueUnavailable {
					w.Header().Set("Retry-After", overloadRetryAfterSeconds)
				}
			}
			writeFailure(w, r, status, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"run": result.Run, "decision": result.Decision})
}
