package clarification

import (
	"agentflow-platform/apps/api/internal/domain"
	"context"
	"encoding/json"
)

// Mutation carries only events actually committed by this operation. Duplicate
// answers have no new events; checkpoints never enter JSON or observation.
type Mutation struct {
	domain.Clarification
	Checkpoint json.RawMessage   `json:"-"`
	Events     []domain.RunEvent `json:"-"`
}

// Store keeps the checkpoint opaque: only Runtime decodes model protocol state.
type Store interface {
	SuspendClarification(context.Context, domain.Clarification, json.RawMessage) (Mutation, error)
	ListClarifications(context.Context, string, string, string) ([]domain.Clarification, error)
	AnswerClarification(context.Context, string, string, string, string, domain.ClarificationAnswer) (Mutation, error)
	ClaimClarification(context.Context, string, string, string, string) (Mutation, error)
	WithdrawClarification(context.Context, string, string, string, string, int64) (Mutation, error)
}
