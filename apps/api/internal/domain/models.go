package domain

import "time"

type Conversation struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Message struct {
	ID             string    `json:"id"`
	ConversationID string    `json:"conversation_id"`
	Role           string    `json:"role"`
	Content        string    `json:"content"`
	CreatedAt      time.Time `json:"created_at"`
}

type Agent struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Description  string    `json:"description"`
	SystemPrompt string    `json:"system_prompt"`
	Tools        []string  `json:"tools"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type RunStatus string

const (
	RunQueued         RunStatus = "queued"
	RunRunning        RunStatus = "running"
	RunWaitingForUser RunStatus = "waiting_for_user"
	RunCompleted      RunStatus = "completed"
	RunFailed         RunStatus = "failed"
	RunCanceling      RunStatus = "canceling"
	RunCanceled       RunStatus = "canceled"
)

type Run struct {
	ID             string     `json:"id"`
	AgentID        string     `json:"agent_id"`
	ConversationID string     `json:"conversation_id"`
	Status         RunStatus  `json:"status"`
	Error          string     `json:"error,omitempty"`
	StartedAt      *time.Time `json:"started_at,omitempty"`
	CompletedAt    *time.Time `json:"completed_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

type CollaborationStepStatus string

const (
	CollaborationStepQueued    CollaborationStepStatus = "queued"
	CollaborationStepRunning   CollaborationStepStatus = "running"
	CollaborationStepCompleted CollaborationStepStatus = "completed"
	CollaborationStepFailed    CollaborationStepStatus = "failed"
)

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
	ConversationID string `json:"conversation_id"`
	AgentID        string `json:"agent_id"`
	Message        string `json:"message"`
	Mode           string `json:"mode"`
}

type ContinueRunRequest struct {
	Plan string `json:"plan"`
}

type ResumeRunRequest struct {
	UserInput string `json:"user_input"`
}

type ChatChunk struct {
	Type           string `json:"type"`
	ConversationID string `json:"conversation_id,omitempty"`
	RunID          string `json:"run_id,omitempty"`
	AgentID        string `json:"agent_id,omitempty"`
	Status         string `json:"status,omitempty"`
	MessageID      string `json:"message_id,omitempty"`
	Delta          string `json:"delta,omitempty"`
	Error          string `json:"error,omitempty"`
	Role           string `json:"role,omitempty"`
	Iteration      int    `json:"iteration,omitempty"`
	MaxIterations  int    `json:"max_iterations,omitempty"`
	ElapsedSeconds int    `json:"elapsed_seconds,omitempty"`
	MaxRuntimeSec  int    `json:"max_runtime_seconds,omitempty"`
	OutputChars    int    `json:"output_chars,omitempty"`
	MaxOutputChars int    `json:"max_output_chars,omitempty"`
	ToolCalls      int    `json:"tool_calls,omitempty"`
	MaxToolCalls   int    `json:"max_tool_calls,omitempty"`
	StopReason     string `json:"stop_reason,omitempty"`
	Question       string `json:"question,omitempty"`
	Context        string `json:"context,omitempty"`
	Input          string `json:"input,omitempty"`
	Output         string `json:"output,omitempty"`
}
