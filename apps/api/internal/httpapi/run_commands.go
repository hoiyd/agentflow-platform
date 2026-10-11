package httpapi

import (
	"context"
	"errors"
	"net/http"

	"agentflow-platform/apps/api/app/runcommands"
	"agentflow-platform/apps/api/internal/domain"
)

const overloadRetryAfterSeconds = "1"

func commandScope(r *http.Request, workspace string) runcommands.Scope {
	return runcommands.Scope{Workspace: domain.NewWorkspaceScope(workspace), Owner: workspaceOwner(r)}
}

func (h *Handler) runCommands() runcommands.Commands {
	return runcommands.Commands{Store: h.store, Runtime: h.agentRuntime, Capacity: h.runController, Verification: h.verification, Memory: h.memories,
		AfterRelease: func(ctx context.Context, scope runcommands.Scope, conversation string) {
			h.scheduleFollowup(ctx, scope, conversation, false)
		},
	}
}

func commandStatus(reason runcommands.Reason) int {
	switch reason {
	case runcommands.Invalid:
		return http.StatusBadRequest
	case runcommands.Missing:
		return http.StatusNotFound
	case runcommands.Conflict:
		return http.StatusConflict
	case runcommands.Routing:
		return http.StatusUnprocessableEntity
	case runcommands.Overload:
		return http.StatusTooManyRequests
	case runcommands.QueueUnavailable:
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

func (h *Handler) executeRunCommand(w http.ResponseWriter, r *http.Request, execute func(runcommands.Observer) error) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming is not supported")
		return
	}
	streaming := false
	begin := func() {
		if streaming {
			return
		}
		streaming = true
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
	}
	err := execute(runcommands.Observer{
		Conversation: func(id string) {
			begin()
			writeSSE(w, "conversation", domain.ChatChunk{Type: "conversation", ConversationID: id})
			flusher.Flush()
		},
		Run: func(run domain.Run) {
			begin()
			writeRunStateSSE(w, flusher, run.ConversationID, run.ID, run.AgentID, run.Status)
		},
		Event: func(event domain.RunEvent) { writeUnifiedRunEvent(w, flusher, event) },
		Done:  func(chunk domain.ChatChunk) { writeSSE(w, "done", chunk); flusher.Flush() },
	})
	if err == nil {
		return
	}
	status := http.StatusInternalServerError
	var commandErr *runcommands.Error
	if errors.As(err, &commandErr) {
		status = commandStatus(commandErr.Reason)
		if commandErr.Reason == runcommands.Overload || commandErr.Reason == runcommands.QueueUnavailable {
			w.Header().Set("Retry-After", overloadRetryAfterSeconds)
		}
	}
	if streaming {
		writeSSE(w, "error", failureChatChunk(w, r, status, err))
		flusher.Flush()
	} else if r.Context().Err() == nil {
		writeFailure(w, r, status, err)
	}
}
