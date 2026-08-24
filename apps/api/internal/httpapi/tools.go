package httpapi

import (
	"net/http"
	"net/url"
	"strings"
)

func (h *Handler) listTools(w http.ResponseWriter, r *http.Request) {
	items, err := h.tools.List()
	if err != nil {
		writeFailure(w, http.StatusInternalServerError, err)
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

	items, err := h.tools.SetEnabled(name, enabled)
	if err != nil {
		writeFailure(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}
