package domain

import "time"

type ClarificationQuestion struct {
	ID       string   `json:"id"`
	Question string   `json:"question"`
	Options  []string `json:"options,omitempty"`
}

// Clarification is a durable input boundary, not an approval or another Run.
// Protocol checkpoints are kept separately and never returned by these DTOs.
type Clarification struct {
	ID             string                  `json:"id"`
	RunID          string                  `json:"run_id"`
	ConversationID string                  `json:"conversation_id"`
	StageID        string                  `json:"stage_id,omitempty"`
	TurnID         string                  `json:"turn_id"`
	ToolCallID     string                  `json:"tool_call_id,omitempty"`
	Version        int64                   `json:"version"`
	Status         string                  `json:"status"`
	Questions      []ClarificationQuestion `json:"questions"`
	Answers        map[string]string       `json:"answers,omitempty"`
	CreatedAt      time.Time               `json:"created_at"`
	ExpiresAt      time.Time               `json:"expires_at"`
	AnsweredAt     *time.Time              `json:"answered_at,omitempty"`
	AppliedAt      *time.Time              `json:"applied_at,omitempty"`
}

type ClarificationAnswer struct {
	Version        int64             `json:"version"`
	IdempotencyKey string            `json:"idempotency_key"`
	Answers        map[string]string `json:"answers"`
}
