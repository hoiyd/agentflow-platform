package httpapi

import (
	"net/http"
	"strings"
)

func (h *Handler) listTools(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.tools.List())
}

func (h *Handler) setToolEnabled(w http.ResponseWriter, r *http.Request, enabled bool) {
	name := strings.TrimPrefix(r.URL.Path, "/api/tools/")
	name = strings.TrimSuffix(name, "/enable")
	name = strings.TrimSuffix(name, "/disable")
	name = strings.TrimSpace(name)
	if name == "" {
		writeError(w, http.StatusBadRequest, "tool name is required")
		return
	}

	if err := h.tools.SetEnabled(name, enabled); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, h.tools.List())
}
