package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"agentflow-platform/apps/api/app/runcommands"
	"agentflow-platform/apps/api/internal/approval"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/store"
)

func approvalStatus(err error) int {
	if errors.Is(err, approval.ErrInvalid) {
		return 400
	}
	if errors.Is(err, approval.ErrConflict) {
		return 409
	}
	if store.IsNotFound(err) {
		return 404
	}
	return 500
}

func (h *Handler) listToolApprovals(w http.ResponseWriter, r *http.Request) {
	if !h.requireClarificationRun(w, r) {
		return
	}
	items, err := h.toolApprovals.ListToolApprovals(r.Context(), workspaceOwner(r), workspaceIDFromRequest(r), r.PathValue("id"))
	if err != nil {
		writeFailure(w, r, approvalStatus(err), err)
		return
	}
	writeJSON(w, 200, items)
}

func (h *Handler) decideToolApproval(w http.ResponseWriter, r *http.Request) {
	if !h.requireClarificationRun(w, r) {
		return
	}
	var input domain.ToolApprovalDecision
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		writeError(w, 400, "invalid Tool approval decision")
		return
	}
	item, err := h.toolApprovals.DecideToolApproval(r.Context(), workspaceOwner(r), workspaceIDFromRequest(r), r.PathValue("id"), r.PathValue("approval_id"), input)
	if err == nil && input.Decision != "approve" {
		_, err = h.agentRuntime.CancelRun(r.PathValue("id"))
	}
	if err != nil {
		writeFailure(w, r, approvalStatus(err), err)
		return
	}
	writeJSON(w, 200, item.ToolApproval)
}

func (h *Handler) resumeToolApproval(w http.ResponseWriter, r *http.Request) {
	if !h.requireClarificationRun(w, r) {
		return
	}
	h.executeRunCommand(w, r, func(observer runcommands.Observer) error {
		return h.runCommands().ResumeToolApproval(r.Context(), commandScope(r, workspaceIDFromRequest(r)), r.PathValue("id"), r.PathValue("approval_id"), observer)
	})
}
