package domain

import "time"

// RunInput is a durable user submission, not a model message or a new budget.
// Applied records an execution boundary; it does not attest model compliance.
type RunInput struct {
	ID             string     `json:"id"`
	ConversationID string     `json:"conversation_id"`
	RunID          string     `json:"run_id"`
	Kind           string     `json:"kind"`
	Content        string     `json:"content"`
	Mode           string     `json:"mode"`
	AgentID        string     `json:"agent_id"`
	Status         string     `json:"status"`
	AppliedRunID   string     `json:"applied_run_id"`
	StageID        string     `json:"stage_id"`
	TurnID         string     `json:"turn_id"`
	CreatedAt      time.Time  `json:"created_at"`
	ExpiresAt      time.Time  `json:"expires_at"`
	AppliedAt      *time.Time `json:"applied_at,omitempty"`
}

type RunInputRequest struct {
	Kind           string `json:"kind"`
	RunID          string `json:"run_id"`
	Content        string `json:"content"`
	Mode           string `json:"mode,omitempty"`
	AgentID        string `json:"agent_id,omitempty"`
	IdempotencyKey string `json:"idempotency_key"`
}
