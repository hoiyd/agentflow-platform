package httpapi

import (
	"net/http"
	"strings"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/verification"
)

func (h *Handler) verifyRun(w http.ResponseWriter, r *http.Request) {
	runID := strings.TrimSpace(r.PathValue("id"))
	if runID == "" {
		writeError(w, http.StatusBadRequest, "run id is required")
		return
	}
	workspaceID := workspaceIDFromRequest(r)
	run, ok, err := h.store.GetRunInWorkspace(workspaceID, runID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "run not found")
		return
	}
	if run.CompletionContract == nil {
		writeError(w, http.StatusConflict, "run does not require verification")
		return
	}
	if run.Status == domain.RunCompleted || run.Status == domain.RunCanceled {
		writeError(w, http.StatusConflict, "terminal run cannot be reverified")
		return
	}
	messages, err := h.store.ListMessagesInWorkspace(workspaceID, run.ConversationID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	output := latestAssistantOutput(messages)
	if output == "" {
		writeError(w, http.StatusConflict, "run has no candidate output to verify")
		return
	}
	decision, err := h.verification.Verify(r.Context(), run.ID, verification.SubjectForRunOutput(output))
	if err != nil {
		_, _ = h.store.UpdateRunVerificationStatus(run.ID, domain.VerificationBlocked)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if decision.AllowCompletion {
		run, err = h.agentRuntime.CompleteRun(run.ID)
	} else {
		run, err = h.agentRuntime.RejectRunCompletion(run.ID, decision.RunStatus, decision.Summary)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"run": run, "decision": decision})
}
