package event

import "agentflow-platform/apps/api/internal/domain"

type RunStatusPayload struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

type RunProgressPayload struct {
	Mode              string `json:"mode"`
	Iteration         int    `json:"iteration,omitempty"`
	MaxIterations     int    `json:"max_iterations,omitempty"`
	ElapsedSeconds    int    `json:"elapsed_seconds,omitempty"`
	MaxRuntimeSeconds int    `json:"max_runtime_seconds,omitempty"`
	OutputChars       int    `json:"output_chars,omitempty"`
	MaxOutputChars    int    `json:"max_output_chars,omitempty"`
	ToolCalls         int    `json:"tool_calls,omitempty"`
	MaxToolCalls      int    `json:"max_tool_calls,omitempty"`
	StopReason        string `json:"stop_reason,omitempty"`
}

type StagePayload struct {
	Name      string `json:"name"`
	AgentID   string `json:"agent_id,omitempty"`
	Iteration int    `json:"iteration,omitempty"`
	Input     string `json:"input,omitempty"`
	Output    string `json:"output,omitempty"`
	Error     string `json:"error,omitempty"`
}

type ModelPayload struct {
	Model               string `json:"model,omitempty"`
	Output              string `json:"output,omitempty"`
	OutputChars         int    `json:"output_chars,omitempty"`
	PromptTokens        int    `json:"prompt_tokens,omitempty"`
	CompletionTokens    int    `json:"completion_tokens,omitempty"`
	TotalTokens         int    `json:"total_tokens,omitempty"`
	TokenUsageEstimated bool   `json:"token_usage_estimated,omitempty"`
	DurationMS          int64  `json:"duration_ms,omitempty"`
	Error               string `json:"error,omitempty"`
}

type ContextAssembledPayload struct {
	Manifest domain.ContextManifest `json:"manifest"`
}

type ToolPayload struct {
	ToolCallID string `json:"tool_call_id"`
	ToolName   string `json:"tool_name"`
	Arguments  any    `json:"arguments,omitempty"`
	Result     any    `json:"result,omitempty"`
	DurationMS int64  `json:"duration_ms,omitempty"`
	Error      string `json:"error,omitempty"`
}

type RetrievalPayload struct {
	Query       string `json:"query,omitempty"`
	MemoryCount int    `json:"memory_count"`
	ChunkCount  int    `json:"chunk_count"`
	Error       string `json:"error,omitempty"`
}
