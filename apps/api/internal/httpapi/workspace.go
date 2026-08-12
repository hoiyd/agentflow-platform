package httpapi

import (
	"context"
	"net/http"
	"strings"
)

const WorkspaceHeader = "X-Workspace-ID"

type WorkspacePolicy struct {
	DefaultID string
	Required  bool
}

type workspaceContextKey struct{}

type requestWorkspace struct {
	ID       string
	Explicit bool
}

func normalizeWorkspacePolicy(policy WorkspacePolicy) WorkspacePolicy {
	policy.DefaultID = strings.TrimSpace(policy.DefaultID)
	if policy.DefaultID == "" {
		policy.DefaultID = "default"
	}
	return policy
}

func (h *Handler) withWorkspace(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			next.ServeHTTP(w, r)
			return
		}
		headerWorkspaceID := strings.TrimSpace(r.Header.Get(WorkspaceHeader))
		queryWorkspaceID := strings.TrimSpace(r.URL.Query().Get("workspace_id"))
		if headerWorkspaceID != "" && queryWorkspaceID != "" && headerWorkspaceID != queryWorkspaceID {
			writeError(w, http.StatusBadRequest, "workspace_id query does not match request header")
			return
		}
		workspaceID := headerWorkspaceID
		explicit := workspaceID != ""
		if workspaceID == "" {
			workspaceID = queryWorkspaceID
			explicit = workspaceID != ""
		}
		if workspaceID == "" && h.workspace.Required {
			writeError(w, http.StatusBadRequest, "workspace_id is required")
			return
		}
		if workspaceID == "" {
			workspaceID = h.workspace.DefaultID
		}
		ctx := context.WithValue(r.Context(), workspaceContextKey{}, requestWorkspace{ID: workspaceID, Explicit: explicit})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func workspaceIDFromRequest(r *http.Request) string {
	workspace, _ := r.Context().Value(workspaceContextKey{}).(requestWorkspace)
	if workspace.ID == "" {
		workspace.ID = strings.TrimSpace(r.Header.Get(WorkspaceHeader))
	}
	if workspace.ID == "" {
		workspace.ID = strings.TrimSpace(r.URL.Query().Get("workspace_id"))
	}
	if workspace.ID == "" {
		workspace.ID = "default"
	}
	return workspace.ID
}

func resolvePayloadWorkspace(r *http.Request, payloadWorkspaceID string) (string, bool) {
	scope, _ := r.Context().Value(workspaceContextKey{}).(requestWorkspace)
	if scope.ID == "" {
		scope.ID = workspaceIDFromRequest(r)
		scope.Explicit = strings.TrimSpace(r.Header.Get(WorkspaceHeader)) != "" || strings.TrimSpace(r.URL.Query().Get("workspace_id")) != ""
	}
	payloadWorkspaceID = strings.TrimSpace(payloadWorkspaceID)
	if payloadWorkspaceID != "" && scope.Explicit && payloadWorkspaceID != scope.ID {
		return "", false
	}
	if payloadWorkspaceID != "" && !scope.Explicit {
		return payloadWorkspaceID, true
	}
	return scope.ID, scope.ID != ""
}
