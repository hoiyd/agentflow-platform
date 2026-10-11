package httpapi

import (
	"encoding/json"
	"net/http"

	"agentflow-platform/apps/api/app/runcommands"
	"agentflow-platform/apps/api/internal/apicontract"
)

func (h *Handler) chat(w http.ResponseWriter, r *http.Request) {
	var input apicontract.ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	req := chatRequestFromContract(input)
	workspace, matches := resolvePayloadWorkspace(r, req.WorkspaceID)
	if !matches {
		writeError(w, http.StatusBadRequest, "workspace_id does not match request scope")
		return
	}
	req.WorkspaceID = workspace
	if rejectCredentialContent(w, r, req) {
		return
	}
	scope := commandScope(r, workspace)
	h.executeRunCommand(w, r, func(observer runcommands.Observer) error {
		return h.runCommands().Start(r.Context(), scope, req, nil, nil, observer)
	})
}
