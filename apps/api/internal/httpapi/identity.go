package httpapi

import (
	"context"
	"errors"
	"net/http"

	"agentflow-platform/apps/api/internal/identity"
	"agentflow-platform/apps/api/internal/inference/requestcontrol"
)

type identityContextKey struct{}

func (h *Handler) withIdentity(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" || isIdentityRoute(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		if h.identity == nil {
			// Local requests have no Session, but share the same trusted owner as
			// local Workspaces, including direct Knowledge/Memory Embedding calls.
			ctx := requestcontrol.WithOwner(r.Context(), identity.SuperUserID)
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}
		user, err := h.identity.Authenticate(r)
		if err != nil {
			status := http.StatusUnauthorized
			if !errors.Is(err, identity.ErrUnauthenticated) {
				status = http.StatusServiceUnavailable
			}
			writeError(w, status, http.StatusText(status))
			return
		}
		if !h.identity.AllowedMutation(r) {
			writeError(w, http.StatusForbidden, "Request origin is not permitted")
			return
		}
		// Identity is server-owned, never copied from headers, query or body.
		ctx := context.WithValue(r.Context(), identityContextKey{}, user)
		ctx = requestcontrol.WithOwner(ctx, user.ID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func isIdentityRoute(path string) bool {
	switch path {
	case "/api/auth/session", "/api/auth/login", "/api/auth/register", "/api/auth/callback", "/api/auth/logout":
		return true
	default:
		return false
	}
}
