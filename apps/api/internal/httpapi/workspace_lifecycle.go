package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"agentflow-platform/apps/api/internal/apicontract"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/identity"
	"agentflow-platform/apps/api/internal/store"
)

func workspaceOwner(r *http.Request) string {
	if user, ok := r.Context().Value(identityContextKey{}).(identity.User); ok {
		return user.ID
	}
	return identity.SuperUserID
}

func (h *Handler) workspaceFailure(w http.ResponseWriter, r *http.Request, err error) {
	status := http.StatusInternalServerError
	var invalid *store.WorkspaceError
	if store.IsNotFound(err) {
		status = http.StatusNotFound
	} else if errors.As(err, &invalid) {
		status = http.StatusBadRequest
		if invalid.Conflict {
			status = http.StatusConflict
		}
	}
	writeFailure(w, r, status, err)
}

func (h *Handler) listWorkspaces(w http.ResponseWriter, r *http.Request) {
	items, err := h.workspaces.ListWorkspaces(r.Context(), workspaceOwner(r))
	if err != nil {
		h.workspaceFailure(w, r, err)
		return
	}
	writeJSON(w, 200, items)
}

func (h *Handler) getWorkspace(w http.ResponseWriter, r *http.Request) {
	item, err := h.workspaces.GetWorkspace(r.Context(), workspaceOwner(r), r.PathValue("id"))
	if err != nil {
		h.workspaceFailure(w, r, err)
		return
	}
	writeJSON(w, 200, item)
}

func decodeWorkspaceBody(w http.ResponseWriter, r *http.Request, value any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		writeError(w, 400, "Invalid Workspace request")
		return false
	}
	var extra any
	if err := decoder.Decode(&extra); errors.Is(err, io.EOF) {
		return true
	}
	writeError(w, 400, "Only one Workspace request body is permitted")
	return false
}

func (h *Handler) createWorkspace(w http.ResponseWriter, r *http.Request) {
	var input apicontract.WorkspaceCreateRequest
	if !decodeWorkspaceBody(w, r, &input) {
		return
	}
	description := ""
	if input.Description != nil {
		description = *input.Description
	}
	item, err := h.workspaces.CreateWorkspace(r.Context(), workspaceOwner(r), input.Name, description)
	if err != nil {
		h.workspaceFailure(w, r, err)
		return
	}
	writeJSON(w, 201, item)
}

func (h *Handler) updateWorkspace(w http.ResponseWriter, r *http.Request) {
	var input apicontract.WorkspaceUpdateRequest
	if !decodeWorkspaceBody(w, r, &input) {
		return
	}
	change := domain.WorkspaceUpdate{Name: input.Name, Description: input.Description}
	if input.Status != nil {
		status := string(*input.Status)
		change.Status = &status
	}
	if input.MakeDefault != nil {
		change.MakeDefault = *input.MakeDefault
	}
	if input.ReplacementWorkspaceId != nil {
		change.ReplacementWorkspaceID = *input.ReplacementWorkspaceId
	}
	item, err := h.workspaces.UpdateWorkspace(r.Context(), workspaceOwner(r), r.PathValue("id"), change, false)
	if err != nil {
		h.workspaceFailure(w, r, err)
		return
	}
	writeJSON(w, 200, item)
}

func (h *Handler) deleteWorkspace(w http.ResponseWriter, r *http.Request) {
	change := domain.WorkspaceUpdate{ReplacementWorkspaceID: r.URL.Query().Get("replacement_workspace_id")}
	_, err := h.workspaces.UpdateWorkspace(r.Context(), workspaceOwner(r), r.PathValue("id"), change, true)
	if err != nil {
		h.workspaceFailure(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
