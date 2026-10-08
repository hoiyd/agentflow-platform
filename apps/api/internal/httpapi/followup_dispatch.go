package httpapi

import (
	"context"
	"errors"
	"log"
	"net/http"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/store"
)

type followupDispatch struct {
	input                 domain.RunInput
	allowStopped, started bool
}

func (h *Handler) scheduleFollowup(r *http.Request, conversation string, allowStopped bool) {
	if h.inbox == nil {
		return
	}
	if err := h.dispatchFollowup(r, conversation, allowStopped); err != nil && !errors.Is(err, store.ErrInputConflict) {
		// No retry loop: retained receipts are visible and can be explicitly retried.
		log.Printf("follow-up dispatch deferred: %v", err)
	}
}

func (h *Handler) dispatchFollowup(r *http.Request, conversation string, allowStopped bool) error {
	r = r.Clone(context.WithoutCancel(r.Context()))
	items, err := h.inbox.ListRunInputs(r.Context(), workspaceOwner(r), workspaceIDFromRequest(r), conversation)
	if err != nil {
		return err
	}
	var input *domain.RunInput
	for i := range items {
		if items[i].Kind == "follow_up" && items[i].Status == "queued" {
			input = &items[i]
			break
		}
	}
	if input == nil {
		return store.ErrInputConflict
	}
	runs, err := h.scopedStore(r).ListRuns()
	if err != nil {
		return err
	}
	eligible := false
	for _, run := range runs {
		if run.ConversationID == conversation {
			eligible = run.Status == domain.RunCompleted || allowStopped && (run.Status == domain.RunFailed || run.Status == domain.RunCanceled)
			break
		}
	}
	if !eligible {
		return store.ErrInputConflict
	}
	// Reserve before spawning: pending workers are bounded by existing admission
	// and participate in application shutdown. StartChat reacquires the same writer.
	reservation, err := h.runController.Reserve()
	if err != nil {
		return err
	}
	go func() {
		defer reservation.Cancel()
		h.startChat(&discardStream{header: make(http.Header)}, r, domain.ChatRequest{ConversationID: conversation, WorkspaceID: workspaceIDFromRequest(r), AgentID: input.AgentID, Mode: input.Mode, Message: input.Content}, reservation, &followupDispatch{input: *input, allowStopped: allowStopped})
	}()
	return nil
}

// Background execution has no browser subscriber; durable events are the output.
type discardStream struct{ header http.Header }

func (w *discardStream) Header() http.Header          { return w.header }
func (*discardStream) Write(data []byte) (int, error) { return len(data), nil }
func (*discardStream) WriteHeader(int)                {}
func (*discardStream) Flush()                         {}
