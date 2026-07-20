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

type ContextCompactionPayload struct {
	CompactionID         string   `json:"compaction_id,omitempty"`
	Trigger              string   `json:"trigger"`
	Status               string   `json:"status"`
	SourceMessageIDs     []string `json:"source_message_ids,omitempty"`
	BeforeTokens         int      `json:"before_tokens,omitempty"`
	AfterTokens          int      `json:"after_tokens,omitempty"`
	ObservedPromptTokens int      `json:"observed_prompt_tokens,omitempty"`
	SummaryModel         string   `json:"summary_model,omitempty"`
	AlgorithmVersion     string   `json:"algorithm_version"`
	Error                string   `json:"error,omitempty"`
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

type MemoryCandidatePayload struct {
	CandidateID      string  `json:"candidate_id,omitempty"`
	SourceMessageID  string  `json:"source_message_id"`
	SourceRole       string  `json:"source_role"`
	Kind             string  `json:"kind,omitempty"`
	Status           string  `json:"status"`
	ExtractionReason string  `json:"extraction_reason,omitempty"`
	PolicyReason     string  `json:"policy_reason,omitempty"`
	Confidence       float64 `json:"confidence,omitempty"`
	Error            string  `json:"error,omitempty"`
}

type UsagePayload struct {
	UsageEntryID        string `json:"usage_entry_id"`
	OperationID         string `json:"operation_id"`
	Kind                string `json:"kind"`
	Purpose             string `json:"purpose"`
	Model               string `json:"model,omitempty"`
	ToolName            string `json:"tool_name,omitempty"`
	ModelCalls          int    `json:"model_calls"`
	ToolCalls           int    `json:"tool_calls"`
	PromptTokens        int    `json:"prompt_tokens"`
	CompletionTokens    int    `json:"completion_tokens"`
	TotalTokens         int    `json:"total_tokens"`
	EstimatedCostMicros int64  `json:"estimated_cost_micros"`
	OpenReservations    int    `json:"open_reservations"`
	UsageEstimated      bool   `json:"usage_estimated"`
}

type BudgetExceededPayload struct {
	Resource    string `json:"resource"`
	Limit       int64  `json:"limit"`
	Used        int64  `json:"used"`
	Requested   int64  `json:"requested"`
	OperationID string `json:"operation_id,omitempty"`
	Purpose     string `json:"purpose,omitempty"`
}
