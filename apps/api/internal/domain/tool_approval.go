package domain

import (
	"encoding/json"
	"time"
)

// ToolApproval authorizes one exact call, not a plan, user message, or wider scope.
// Protocol continuation and execution-claim secrets are never public metadata.
type ToolApproval struct {
	ID                 string          `json:"id"`
	RunID              string          `json:"run_id"`
	ConversationID     string          `json:"conversation_id"`
	WorkspaceID        string          `json:"workspace_id"`
	OwnerID            string          `json:"owner_id"`
	StageID            string          `json:"stage_id,omitempty"`
	TurnID             string          `json:"turn_id"`
	ToolCallID         string          `json:"tool_call_id"`
	Tool               string          `json:"tool"`
	Arguments          json.RawMessage `json:"arguments"`
	ArgumentsHash      string          `json:"arguments_hash"`
	DefinitionRevision string          `json:"definition_revision"`
	PolicyHash         string          `json:"policy_hash"`
	SnapshotHash       string          `json:"snapshot_hash"`
	Version            int64           `json:"version"`
	Status             string          `json:"status"`
	CreatedAt          time.Time       `json:"created_at"`
	ExpiresAt          time.Time       `json:"expires_at"`
	DecidedAt          *time.Time      `json:"decided_at,omitempty"`
}

type ToolApprovalDecision struct {
	Version        int64  `json:"version"`
	Decision       string `json:"decision"`
	IdempotencyKey string `json:"idempotency_key"`
}
