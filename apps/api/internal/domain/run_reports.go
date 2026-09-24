package domain

// RunReplay is the detailed aggregate assembled from persisted Run records.
type RunReplay struct {
	Run                   Run                    `json:"run"`
	Projection            RunProjectionSnapshot  `json:"projection"`
	RuntimeSnapshot       *RuntimeSnapshot       `json:"runtime_snapshot,omitempty"`
	Conversation          Conversation           `json:"conversation"`
	Messages              []Message              `json:"messages"`
	Steps                 []CollaborationStep    `json:"steps"`
	Summary               RunTraceSummary        `json:"summary"`
	UsageLedger           RunUsageLedger         `json:"usage_ledger"`
	RunEvents             []RunEvent             `json:"run_events"`
	StageCheckpoints      []StageCheckpoint      `json:"stage_checkpoints"`
	ToolEffects           []ToolEffectSummary    `json:"tool_effects"`
	ToolArtifacts         []ToolArtifact         `json:"tool_artifacts"`
	VerificationEvidence  []VerificationEvidence `json:"verification_evidence"`
	VerificationArtifacts []VerificationArtifact `json:"verification_artifacts"`
	TaskStateRevisions    []TaskStateRevision    `json:"task_state_revisions"`
	RecoverySummary       *RecoverySummary       `json:"recovery_summary,omitempty"`
}

// EpisodeReport is a compact projection derived from RunReplay for review,
// export, and offline evaluation. It is not an independent source of truth.
type EpisodeReport struct {
	Run          Run                 `json:"run"`
	Conversation Conversation        `json:"conversation"`
	Agent        Agent               `json:"agent"`
	Task         string              `json:"task"`
	FinalOutput  string              `json:"final_output"`
	Messages     []Message           `json:"messages"`
	Steps        []CollaborationStep `json:"steps"`
	TraceSummary RunTraceSummary     `json:"trace_summary"`
	Retrievals   EpisodeRetrievals   `json:"retrievals"`
	LLMCalls     []EpisodeLLMCall    `json:"llm_calls"`
	ToolCalls    []EpisodeToolCall   `json:"tool_calls"`
	Errors       []EpisodeError      `json:"errors"`
	Verification EpisodeVerification `json:"verification"`
}

type EpisodeRetrievals struct {
	EventCount int              `json:"event_count"`
	Memories   []map[string]any `json:"memories"`
	Chunks     []map[string]any `json:"chunks"`
}

type EpisodeLLMCall struct {
	EventID             string `json:"event_id"`
	StepID              string `json:"step_id,omitempty"`
	Role                string `json:"role,omitempty"`
	AgentID             string `json:"agent_id,omitempty"`
	Model               string `json:"model,omitempty"`
	Framework           string `json:"framework,omitempty"`
	PromptTokens        int    `json:"prompt_tokens,omitempty"`
	CompletionTokens    int    `json:"completion_tokens,omitempty"`
	TotalTokens         int    `json:"total_tokens,omitempty"`
	TokenUsageEstimated bool   `json:"token_usage_estimated,omitempty"`
	OutputChars         int    `json:"output_chars,omitempty"`
	DurationMS          int64  `json:"duration_ms,omitempty"`
}

type EpisodeToolCall struct {
	EventID    string `json:"event_id"`
	StepID     string `json:"step_id,omitempty"`
	ToolName   string `json:"tool_name,omitempty"`
	ToolCallID string `json:"tool_call_id,omitempty"`
	Error      string `json:"error,omitempty"`
	DurationMS int64  `json:"duration_ms,omitempty"`
}

type EpisodeError struct {
	Source    string `json:"source"`
	EventID   string `json:"event_id,omitempty"`
	StepID    string `json:"step_id,omitempty"`
	Kind      string `json:"kind,omitempty"`
	Category  string `json:"category,omitempty"`
	Retryable *bool  `json:"retryable,omitempty"`
	Message   string `json:"message"`
}

type EpisodeVerification struct {
	Status      VerificationStatus     `json:"status"`
	SubjectHash string                 `json:"subject_hash,omitempty"`
	Contract    *CompletionContract    `json:"contract,omitempty"`
	Evidence    []string               `json:"evidence"`
	Warnings    []string               `json:"warnings"`
	Records     []VerificationEvidence `json:"records"`
	Artifacts   []VerificationArtifact `json:"artifacts"`
}
