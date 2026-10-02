package httpapi

import (
	"errors"
	"io"
	"net/http"
	"strings"

	"agentflow-platform/apps/api/internal/identity"
	"agentflow-platform/apps/api/internal/store"
)

// All SSE routes share this delivery guard, including Chat/Continue/Resume.
// Denying a writer never cancels its separately owned durable execution context.
type authorizedStreamWriter struct {
	http.ResponseWriter
	flusher http.Flusher
	handler *Handler
	request *http.Request
	blocked bool
}

func (w *authorizedStreamWriter) Write(data []byte) (int, error) {
	if w.blocked || w.request.Context().Err() != nil {
		return 0, io.ErrClosedPipe
	}
	if strings.HasPrefix(w.Header().Get("Content-Type"), "text/event-stream") {
		if status := w.handler.streamAccessStatus(w.request); status != 0 {
			w.blocked = true
			_ = writeSSEFrame(w.ResponseWriter, 0, "error", failureChatChunk(w.ResponseWriter, w.request, status, errors.New(http.StatusText(status))))
			w.flusher.Flush()
			return 0, io.ErrClosedPipe
		}
	}
	return w.ResponseWriter.Write(data)
}

func (w *authorizedStreamWriter) Flush() { w.flusher.Flush() }

func (h *Handler) streamAccessStatus(r *http.Request) int {
	owner := workspaceOwner(r)
	if h.identity != nil {
		user, err := h.identity.Authenticate(r)
		if errors.Is(err, identity.ErrUnauthenticated) {
			return http.StatusUnauthorized
		}
		if err != nil {
			return http.StatusServiceUnavailable
		}
		if user.ID != owner {
			return http.StatusUnauthorized
		}
	}
	workspaceID := workspaceIDFromRequest(r)
	if h.workspaces != nil {
		if _, err := h.workspaces.GetWorkspace(r.Context(), owner, workspaceID); err != nil {
			if store.IsNotFound(err) {
				return http.StatusNotFound
			}
			return http.StatusServiceUnavailable
		}
	} else if h.identity != nil {
		allowed, err := h.identity.IsMember(r.Context(), owner, workspaceID)
		if err != nil {
			return http.StatusServiceUnavailable
		}
		if !allowed {
			return http.StatusNotFound
		}
	}
	return 0
}
