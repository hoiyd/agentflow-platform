package httpapi

import (
	"context"
	"errors"
	"net/http"

	"agentflow-platform/apps/api/internal/identity"
)

type identityContextKey struct{}

func (h *Handler) withIdentity(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h.identity == nil || r.URL.Path == "/health" || isIdentityRoute(r.URL.Path) {
			next.ServeHTTP(w, r)
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
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func isIdentityRoute(path string) bool {
	switch path {
	case "/api/auth/session", "/api/auth/login", "/api/auth/callback", "/api/auth/logout":
		return true
	default:
		return false
	}
}
