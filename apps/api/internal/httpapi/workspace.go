package httpapi

import (
	"context"
	"net/http"
	"strings"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/identity"
	"agentflow-platform/apps/api/internal/store"
)

const WorkspaceHeader = "X-Workspace-ID"

type workspaceContextKey struct{}

type requestWorkspace struct {
	ID       string
	Explicit bool
}

func (h *Handler) withWorkspace(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" || isIdentityRoute(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		headerWorkspaceID := strings.TrimSpace(r.Header.Get(WorkspaceHeader))
		queryWorkspaceID := strings.TrimSpace(r.URL.Query().Get("workspace_id"))
		if headerWorkspaceID != "" && queryWorkspaceID != "" && domain.NormalizeWorkspaceID(headerWorkspaceID) != domain.NormalizeWorkspaceID(queryWorkspaceID) {
			writeError(w, http.StatusBadRequest, "workspace_id query does not match request header")
			return
		}
		workspaceID := headerWorkspaceID
		explicit := workspaceID != ""
		if workspaceID == "" {
			workspaceID = queryWorkspaceID
			explicit = workspaceID != ""
		}
		workspaceID = domain.NormalizeWorkspaceID(workspaceID)
		if h.identity != nil {
			user, ok := r.Context().Value(identityContextKey{}).(identity.User)
			if !ok {
				writeError(w, http.StatusUnauthorized, "Authentication required")
				return
			}
			allowed, err := h.identity.IsMember(r.Context(), user.ID, workspaceID)
			if err != nil {
				writeError(w, http.StatusServiceUnavailable, "Membership storage is unavailable")
				return
			}
			if !allowed {
				writeError(w, http.StatusForbidden, "Workspace membership required")
				return
			}
			// Authenticated scope must also bind payload-only Workspace fields.
			explicit = true
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
	return domain.NormalizeWorkspaceID(workspace.ID)
}

func (h *Handler) scopedStore(r *http.Request) store.WorkspaceStore {
	return h.store.ForWorkspace(domain.NewWorkspaceScope(workspaceIDFromRequest(r)))
}

func (h *Handler) scopedStoreForID(workspaceID string) store.WorkspaceStore {
	return h.store.ForWorkspace(domain.NewWorkspaceScope(workspaceID))
}

func resolvePayloadWorkspace(r *http.Request, payloadWorkspaceID string) (string, bool) {
	scope, _ := r.Context().Value(workspaceContextKey{}).(requestWorkspace)
	if scope.ID == "" {
		scope.ID = workspaceIDFromRequest(r)
		scope.Explicit = strings.TrimSpace(r.Header.Get(WorkspaceHeader)) != "" || strings.TrimSpace(r.URL.Query().Get("workspace_id")) != ""
	}
	payloadWorkspaceID = strings.TrimSpace(payloadWorkspaceID)
	if payloadWorkspaceID != "" {
		payloadWorkspaceID = domain.NormalizeWorkspaceID(payloadWorkspaceID)
	}
	if payloadWorkspaceID != "" && scope.Explicit && payloadWorkspaceID != scope.ID {
		return "", false
	}
	if payloadWorkspaceID != "" && !scope.Explicit {
		return payloadWorkspaceID, true
	}
	return scope.ID, scope.ID != ""
}
