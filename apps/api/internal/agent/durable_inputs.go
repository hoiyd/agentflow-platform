package agent

import (
	"context"

	"agentflow-platform/apps/api/internal/domain"
)

// RuntimeInbox persists input application at the shared execution boundary.
// It does not select models, mutate policy, or execute queued Runs itself.
type RuntimeInbox interface {
	ConsumeSteering(context.Context, string, string, string) ([]domain.RunInput, error)
	CreateInputRun(context.Context, string, string, bool, string, string, domain.RuntimeSnapshot, *domain.CompletionContract) (domain.Run, error)
}

type followupKey struct{}
type followupInput struct {
	id, owner    string
	allowStopped bool
}

// WithFollowupInput is used only by the application dispatcher after admission.
// Its owner must come from the authenticated request, never a submitted JSON field.
func WithFollowupInput(ctx context.Context, id, owner string, allowStopped bool) context.Context {
	return context.WithValue(ctx, followupKey{}, followupInput{id, owner, allowStopped})
}

func (r *Runtime) createRun(ctx context.Context, agentID, conversationID string, snapshot domain.RuntimeSnapshot, contract *domain.CompletionContract) (domain.Run, error) {
	if input, ok := ctx.Value(followupKey{}).(followupInput); ok && r.inbox != nil {
		return r.inbox.CreateInputRun(ctx, input.id, input.owner, input.allowStopped, agentID, conversationID, snapshot, contract)
	}
	return r.store.CreateRunWithContract(agentID, conversationID, snapshot, contract)
}
