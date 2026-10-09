package httpapi

import (
	"net/http"
	"net/url"
	"strings"

	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/tool/availability"
)

func (h *Handler) listTools(w http.ResponseWriter, r *http.Request) {
	service, err := h.tools.Catalog()
	if err != nil {
		writeFailure(w, r, http.StatusInternalServerError, err)
		return
	}
	_, items, err := availability.Resolve(h.store, workspaceIDFromRequest(r), service)
	if err != nil {
		writeFailure(w, r, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (h *Handler) setToolEnabled(w http.ResponseWriter, r *http.Request, enabled bool) {
	name := strings.TrimPrefix(r.URL.Path, "/api/tools/")
	name = strings.TrimSuffix(name, "/enable")
	name = strings.TrimSuffix(name, "/disable")
	if unescaped, err := url.PathUnescape(name); err == nil {
		name = unescaped
	}
	name = strings.TrimSpace(name)
	if name == "" {
		writeError(w, http.StatusBadRequest, "tool name is required")
		return
	}

	service, err := h.tools.Catalog()
	if err != nil {
		writeFailure(w, r, http.StatusInternalServerError, err)
		return
	}
	if _, ok := service.Installed(name); !ok {
		writeError(w, http.StatusNotFound, "tool not installed")
		return
	}
	if enabled {
		if _, ok := service.ResolveReady(name); !ok {
			writeError(w, http.StatusForbidden, "Tool disabled or unavailable at service level")
			return
		}
	}
	if _, _, err = availability.Resolve(h.store, workspaceIDFromRequest(r), service); err != nil {
		writeFailure(w, r, http.StatusInternalServerError, err)
		return
	}
	err = h.store.SetWorkspaceToolEnabled(workspaceIDFromRequest(r), name, enabled)
	if err != nil {
		status := http.StatusInternalServerError
		if store.IsNotFound(err) {
			status = http.StatusNotFound
		}
		writeFailure(w, r, status, err)
		return
	}
	h.listTools(w, r)
}
