package httpapi

import (
	"context"
	"net/http"

	"agentflow-platform/apps/api/internal/domain"
)

type ProjectGuidanceDiscovery interface {
	Projects(context.Context, string) (domain.ProjectGuidanceCatalog, error)
	Files(context.Context, string, string, string) (domain.ProjectFilePage, error)
}

func (h *Handler) listGuidanceProjects(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	if h.projectGuidance == nil {
		writeJSON(w, http.StatusOK, domain.ProjectGuidanceCatalog{Projects: []domain.ProjectGuidanceProject{}})
		return
	}
	result, err := h.projectGuidance.Projects(r.Context(), workspaceIDFromRequest(r))
	if err != nil {
		writeFailure(w, r, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) listGuidanceProjectFiles(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	if h.projectGuidance == nil {
		writeError(w, http.StatusServiceUnavailable, "Project instructions are not configured")
		return
	}
	directory := r.URL.Query().Get("path")
	if directory == "" {
		directory = "."
	}
	result, err := h.projectGuidance.Files(r.Context(), workspaceIDFromRequest(r), r.PathValue("root"), directory)
	if err != nil {
		writeFailure(w, r, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
