package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"

	"agentflow-platform/apps/api/app/runcommands"
	"agentflow-platform/apps/api/internal/apicontract"
)

func (h *Handler) continueRun(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/runs/"), "/continue"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "run id is required")
		return
	}
	var req apicontract.ContinueRunRequest
	if h.hasBlockingClarification(w, r, id) {
		return
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if rejectCredentialContent(w, r, req) {
		return
	}
	h.executeRunCommand(w, r, func(observer runcommands.Observer) error {
		return h.runCommands().Continue(r.Context(), commandScope(r, workspaceIDFromRequest(r)), id, req.Plan, routingRequirementsFromInput(req.RoutingRequirements), observer)
	})
}

func (h *Handler) resumeRun(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/runs/"), "/resume"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "run id is required")
		return
	}
	var req apicontract.ResumeRunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if rejectCredentialContent(w, r, req) {
		return
	}
	h.executeRunCommand(w, r, func(observer runcommands.Observer) error {
		return h.runCommands().Resume(r.Context(), commandScope(r, workspaceIDFromRequest(r)), id, req.UserInput, observer)
	})
}
