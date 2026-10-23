package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"agentflow-platform/apps/api/internal/apicontract"
	"agentflow-platform/apps/api/internal/mcpconnection"
	"agentflow-platform/apps/api/internal/store"
)

func (h *Handler) registerMCPConnectionRoutes(mux *http.ServeMux) {
	if h.mcpConnections == nil {
		return
	}
	mux.HandleFunc("GET /api/mcp/connections", h.listMCPConnections)
	mux.HandleFunc("POST /api/mcp/connections", h.saveMCPConnection)
	mux.HandleFunc("GET /api/mcp/connections/{id}", h.getMCPConnection)
	mux.HandleFunc("PATCH /api/mcp/connections/{id}", h.saveMCPConnection)
	mux.HandleFunc("DELETE /api/mcp/connections/{id}", h.deleteMCPConnection)
	mux.HandleFunc("POST /api/mcp/connections/{id}/test", h.testMCPConnection)
}

func (h *Handler) listMCPConnections(w http.ResponseWriter, r *http.Request) {
	items, err := h.mcpConnections.List(r.Context(), workspaceIDFromRequest(r))
	if err != nil {
		writeMCPConnectionFailure(w, r, err)
		return
	}
	writeJSON(w, 200, items)
}

func (h *Handler) getMCPConnection(w http.ResponseWriter, r *http.Request) {
	item, err := h.mcpConnections.Get(workspaceIDFromRequest(r), r.PathValue("id"))
	if err != nil {
		writeMCPConnectionFailure(w, r, err)
		return
	}
	writeJSON(w, 200, item)
}

func (h *Handler) saveMCPConnection(w http.ResponseWriter, r *http.Request) {
	// Credentials are an intentional write-only input here. Do not send the
	// body to generic request capture, credential diagnostics, or logging.
	var input apicontract.MCPConnectionRequest
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeError(w, 400, "Invalid MCP connection request")
		return
	}
	if decoder.Decode(new(any)) != io.EOF {
		writeError(w, 400, "Invalid MCP connection request")
		return
	}
	revision := int64(0)
	if input.Revision != nil {
		revision = *input.Revision
	}
	item, err := h.mcpConnections.Save(r.Context(), workspaceIDFromRequest(r), r.PathValue("id"), mcpconnection.Input{Name: input.Name, URL: input.Url, AuthMode: string(input.AuthMode), BearerToken: input.BearerToken, Enabled: input.Enabled, ReadOnlyTools: input.ReadOnlyTools, Revision: revision})
	if err != nil {
		writeMCPConnectionFailure(w, r, err)
		return
	}
	status := 200
	if r.Method == http.MethodPost {
		status = 201
	}
	writeJSON(w, status, item)
}

func (h *Handler) testMCPConnection(w http.ResponseWriter, r *http.Request) {
	item, err := h.mcpConnections.Test(r.Context(), workspaceIDFromRequest(r), r.PathValue("id"))
	if err != nil {
		writeMCPConnectionFailure(w, r, err)
		return
	}
	writeJSON(w, 200, item)
}

func (h *Handler) deleteMCPConnection(w http.ResponseWriter, r *http.Request) {
	if err := h.mcpConnections.Delete(r.Context(), workspaceIDFromRequest(r), r.PathValue("id")); err != nil {
		writeMCPConnectionFailure(w, r, err)
		return
	}
	w.WriteHeader(204)
}

func writeMCPConnectionFailure(w http.ResponseWriter, r *http.Request, err error) {
	status := 500
	var connectionError *mcpconnection.Error
	var workspaceError *store.WorkspaceError
	switch {
	case store.IsNotFound(err):
		status = 404
	case errors.As(err, &connectionError):
		status = connectionError.Status
	case errors.As(err, &workspaceError) && workspaceError.Conflict:
		status = 409
	}
	if status == 429 {
		w.Header().Set("Retry-After", "2")
	}
	writeFailure(w, r, status, err)
}
