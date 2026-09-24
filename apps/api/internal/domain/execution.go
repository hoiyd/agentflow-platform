package domain

import "time"

type RunStatus string

const (
	RunQueued            RunStatus = "queued"
	RunRunning           RunStatus = "running"
	RunWaitingForUser    RunStatus = "waiting_for_user"
	RunCompleted         RunStatus = "completed"
	RunFailed            RunStatus = "failed"
	RunFailedRecoverable RunStatus = "failed_recoverable"
	RunCanceling         RunStatus = "canceling"
	RunCanceled          RunStatus = "canceled"
)

type Run struct {
	ID                 string              `json:"id"`
	WorkspaceID        string              `json:"workspace_id"`
	AgentID            string              `json:"agent_id"`
	ConversationID     string              `json:"conversation_id"`
	Status             RunStatus           `json:"status"`
	RuntimeSnapshot    *RuntimeSnapshot    `json:"runtime_snapshot,omitempty"`
	CompletionContract *CompletionContract `json:"completion_contract,omitempty"`
	VerificationStatus VerificationStatus  `json:"verification_status"`
	Error              string              `json:"error,omitempty"`
	StartedAt          *time.Time          `json:"started_at,omitempty"`
	ExecutionStartedAt *time.Time          `json:"execution_started_at,omitempty"`
	ActiveRuntimeMS    int64               `json:"active_runtime_ms"`
	HeartbeatAt        *time.Time          `json:"heartbeat_at,omitempty"`
	CompletedAt        *time.Time          `json:"completed_at,omitempty"`
	CreatedAt          time.Time           `json:"created_at"`
	UpdatedAt          time.Time           `json:"updated_at"`
}

type CollaborationStepStatus string

const (
	CollaborationStepQueued    CollaborationStepStatus = "queued"
	CollaborationStepRunning   CollaborationStepStatus = "running"
	CollaborationStepCompleted CollaborationStepStatus = "completed"
	CollaborationStepFailed    CollaborationStepStatus = "failed"
)

// CollaborationStep is the persisted record for one orchestration Stage.
// Related RunEvents reference its ID through StageID; it is not a separate
// execution layer from Stage.
type CollaborationStep struct {
	ID             string                  `json:"id"`
	RunID          string                  `json:"run_id"`
	ConversationID string                  `json:"conversation_id"`
	Role           string                  `json:"role"`
	AgentID        string                  `json:"agent_id,omitempty"`
	Status         CollaborationStepStatus `json:"status"`
	Iteration      int                     `json:"iteration,omitempty"`
	Input          string                  `json:"input"`
	Output         string                  `json:"output"`
	Error          string                  `json:"error,omitempty"`
	CreatedAt      time.Time               `json:"created_at"`
	UpdatedAt      time.Time               `json:"updated_at"`
}

type ChatRequest struct {
	WorkspaceID    string `json:"workspace_id,omitempty"`
	ConversationID string `json:"conversation_id"`
	AgentID        string `json:"agent_id"`
	Message        string `json:"message"`
	Mode           string `json:"mode"`
	// CompletionContract explicitly enables Verification for the new Run.
	// Omitting it leaves verification_status=not_required in every mode.
	CompletionContract *CompletionContract `json:"completion_contract,omitempty"`
}

type ContinueRunRequest struct {
	Plan string `json:"plan"`
}

type ResumeRunRequest struct {
	UserInput string `json:"user_input"`
}

type ChatChunk struct {
	Type               string        `json:"type"`
	ConversationID     string        `json:"conversation_id,omitempty"`
	Title              string        `json:"title,omitempty"`
	RunID              string        `json:"run_id,omitempty"`
	AgentID            string        `json:"agent_id,omitempty"`
	Status             string        `json:"status,omitempty"`
	VerificationStatus string        `json:"verification_status,omitempty"`
	MessageID          string        `json:"message_id,omitempty"`
	Citations          []RAGCitation `json:"citations,omitempty"`
	InvalidCitationIDs []string      `json:"invalid_citation_ids,omitempty"`
	Delta              string        `json:"delta,omitempty"`
	Error              string        `json:"error,omitempty"`
	ErrorCode          string        `json:"code,omitempty"`
	ErrorSource        string        `json:"source,omitempty"`
	ErrorCategory      string        `json:"category,omitempty"`
	Retryable          *bool         `json:"retryable,omitempty"`
	RequestID          string        `json:"request_id,omitempty"`
}

type RunTraceSummary struct {
	RunID               string    `json:"run_id"`
	Status              RunStatus `json:"status"`
	TotalDurationMS     int64     `json:"total_duration_ms"`
	TotalTokens         int       `json:"total_tokens"`
	PromptTokens        int       `json:"prompt_tokens"`
	CompletionTokens    int       `json:"completion_tokens"`
	TokenUsageEstimated bool      `json:"token_usage_estimated"`
	LLMCalls            int       `json:"llm_calls"`
	ToolCalls           int       `json:"tool_calls"`
	ErrorCount          int       `json:"error_count"`
}
