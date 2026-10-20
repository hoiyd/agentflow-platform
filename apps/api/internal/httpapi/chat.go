package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"

	"agentflow-platform/apps/api/app/runcommands"
	"agentflow-platform/apps/api/internal/apicontract"
	"agentflow-platform/apps/api/internal/rag/eligibility"
)

func (h *Handler) chat(w http.ResponseWriter, r *http.Request) {
	var input apicontract.ChatRequest
	decoder, err := documentJSONDecoder(r.Body, 2<<20)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid chat json body")
		return
	}
	var envelope map[string]json.RawMessage
	if decoder.Decode(&envelope) != nil {
		writeError(w, http.StatusBadRequest, "invalid chat json body")
		return
	}
	for key, value := range envelope {
		if strings.EqualFold(key, "knowledge_filter") {
			if err := eligibility.ValidateFilterJSON(value); err != nil {
				writeKnowledgeError(w, r, err)
				return
			}
		}
	}
	data, _ := json.Marshal(envelope)
	decoder = json.NewDecoder(strings.NewReader(string(data)))
	decoder.UseNumber()
	if err := decoder.Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if input.ExecutionConstraint != nil && !input.ExecutionConstraint.Valid() {
		writeError(w, http.StatusBadRequest, "unsupported execution_constraint")
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
