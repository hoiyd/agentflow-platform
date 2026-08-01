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

const (
	LegacyRuntimeSnapshotVersion     = 1
	ContextRuntimeSnapshotVersion    = 2
	CompactionRuntimeSnapshotVersion = 3
	RunBudgetRuntimeSnapshotVersion  = 4
	CurrentRuntimeSnapshotVersion    = 5
)

type RuntimeSnapshot struct {
	SchemaVersion    int                    `json:"schema_version"`
	Mode             string                 `json:"mode"`
	Agent            RuntimeAgentSnapshot   `json:"agent"`
	CandidateAgents  []RuntimeAgentSnapshot `json:"candidate_agents,omitempty"`
	Model            RuntimeModelSnapshot   `json:"model"`
	Tools            []RuntimeToolSnapshot  `json:"tools"`
	ContextAssembly  ContextAssemblyConfig  `json:"context_assembly"`
	RouterMode       string                 `json:"router_mode,omitempty"`
	AutonomousLimits *RuntimeLimitsSnapshot `json:"autonomous_limits,omitempty"`
	RunBudget        *RuntimeRunBudget      `json:"run_budget,omitempty"`
	CreatedAt        time.Time              `json:"created_at"`
}

type ContextAssemblyConfig struct {
	AssemblerVersion           string  `json:"assembler_version"`
	ContextWindowTokens        int     `json:"context_window_tokens"`
	OutputReserveTokens        int     `json:"output_reserve_tokens"`
	SafetyMarginTokens         int     `json:"safety_margin_tokens"`
	HistoryMaxTokens           int     `json:"history_max_tokens"`
	MemoryMaxTokens            int     `json:"memory_max_tokens"`
	KnowledgeMaxTokens         int     `json:"knowledge_max_tokens"`
	ToolResultMaxTokens        int     `json:"compaction_tool_result_max_tokens"`
	CompactionMode             string  `json:"compaction_mode"`
	CompactionSoftThreshold    float64 `json:"compaction_soft_threshold"`
	CompactionHardThreshold    float64 `json:"compaction_hard_threshold"`
	CompactionRecentTokens     int     `json:"compaction_recent_tokens"`
	CompactionSummaryMaxTokens int     `json:"compaction_summary_max_tokens"`
	CompactionTimeoutMS        int64   `json:"compaction_timeout_ms"`
}

type ContextCompaction struct {
	ID               string    `json:"id"`
	ConversationID   string    `json:"conversation_id"`
	RunID            string    `json:"run_id"`
	Trigger          string    `json:"trigger"`
	Summary          string    `json:"summary"`
	SourceMessageIDs []string  `json:"source_message_ids"`
	SourceHash       string    `json:"source_hash"`
	BeforeTokens     int       `json:"before_tokens"`
	AfterTokens      int       `json:"after_tokens"`
	SummaryModel     string    `json:"summary_model"`
	AlgorithmVersion string    `json:"algorithm_version"`
	CreatedAt        time.Time `json:"created_at"`
}

type RuntimeAgentSnapshot struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	Description      string   `json:"description"`
	SystemPrompt     string   `json:"system_prompt"`
	Tools            []string `json:"tools"`
	MemoryEnabled    bool     `json:"memory_enabled"`
	RetrievalEnabled bool     `json:"retrieval_enabled"`
	Executor         string   `json:"executor"`
}

type RuntimeModelSnapshot struct {
	Provider            string `json:"provider"`
	BaseURL             string `json:"base_url"`
	Model               string `json:"model"`
	EmbeddingBaseURL    string `json:"embedding_base_url"`
	EmbeddingModel      string `json:"embedding_model"`
	EmbeddingDimensions int    `json:"embedding_dimensions"`
}

type RuntimeToolSnapshot struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type RuntimeLimitsSnapshot struct {
	MaxIterations  int   `json:"max_iterations"`
	MaxRuntimeMS   int64 `json:"max_runtime_ms"`
	MaxOutputChars int   `json:"max_output_chars"`
	MaxToolCalls   int   `json:"max_tool_calls"`
}

// RuntimeRunBudget is frozen with a Run. Zero disables the corresponding
// limit or price, so later environment changes cannot alter an existing Run.
type RuntimeRunBudget struct {
	MaxModelCalls                    int   `json:"max_model_calls,omitempty"`
	MaxPromptTokens                  int   `json:"max_prompt_tokens,omitempty"`
	MaxCompletionTokens              int   `json:"max_completion_tokens,omitempty"`
	MaxTotalTokens                   int   `json:"max_total_tokens,omitempty"`
	MaxToolCalls                     int   `json:"max_tool_calls,omitempty"`
	MaxRuntimeMS                     int64 `json:"max_runtime_ms,omitempty"`
	MaxEstimatedCostMicros           int64 `json:"max_estimated_cost_micros,omitempty"`
	InputCostPerMillionTokensMicros  int64 `json:"input_cost_per_million_tokens_micros,omitempty"`
	OutputCostPerMillionTokensMicros int64 `json:"output_cost_per_million_tokens_micros,omitempty"`
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
	Executor       string `json:"executor"`
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
}

type RunEventType string

const (
	EventRunCreated              RunEventType = "run.created"
	EventRunStarted              RunEventType = "run.started"
	EventRunProgress             RunEventType = "run.progress"
	EventRunWaitingForUser       RunEventType = "run.waiting_for_user"
	EventRunResumed              RunEventType = "run.resumed"
	EventRunCancelRequested      RunEventType = "run.cancel_requested"
	EventRunCanceled             RunEventType = "run.canceled"
	EventRunCompleted            RunEventType = "run.completed"
	EventRunFailed               RunEventType = "run.failed"
	EventStageStarted            RunEventType = "stage.started"
	EventStageCompleted          RunEventType = "stage.completed"
	EventStageFailed             RunEventType = "stage.failed"
	EventStageCanceled           RunEventType = "stage.canceled"
	EventTurnStarted             RunEventType = "turn.started"
	EventTurnCompleted           RunEventType = "turn.completed"
	EventTurnFailed              RunEventType = "turn.failed"
	EventTurnCanceled            RunEventType = "turn.canceled"
	EventModelStarted            RunEventType = "model.started"
	EventModelDelta              RunEventType = "model.delta"
	EventModelCompleted          RunEventType = "model.completed"
	EventModelFailed             RunEventType = "model.failed"
	EventContextAssembled        RunEventType = "context.assembled"
	EventCompactionStarted       RunEventType = "context.compaction_started"
	EventCompactionCompleted     RunEventType = "context.compaction_completed"
	EventCompactionFailed        RunEventType = "context.compaction_failed"
	EventToolStarted             RunEventType = "tool.started"
	EventToolCompleted           RunEventType = "tool.completed"
	EventToolFailed              RunEventType = "tool.failed"
	EventRetrievalStarted        RunEventType = "retrieval.started"
	EventRetrievalCompleted      RunEventType = "retrieval.completed"
	EventRetrievalFailed         RunEventType = "retrieval.failed"
	EventCitationResolved        RunEventType = "citation.resolved"
	EventMemoryCandidateProposed RunEventType = "memory.candidate.proposed"
	EventMemoryCandidateAccepted RunEventType = "memory.candidate.accepted"
	EventMemoryCandidateRejected RunEventType = "memory.candidate.rejected"
	EventMemoryCandidateFailed   RunEventType = "memory.candidate.failed"
	EventMemorySyncRequested     RunEventType = "memory.sync.requested"
	EventMemorySyncCompleted     RunEventType = "memory.sync.completed"
	EventMemorySyncFailed        RunEventType = "memory.sync.failed"
	EventVerificationRequested   RunEventType = "verification.requested"
	EventVerificationStarted     RunEventType = "verification.started"
	EventVerificationPassed      RunEventType = "verification.passed"
	EventVerificationFailed      RunEventType = "verification.failed"
	EventVerificationBlocked     RunEventType = "verification.blocked"
	EventVerificationStale       RunEventType = "verification.stale"
	EventRunRevisionRequested    RunEventType = "run.revision_requested"
	EventUsageRecorded           RunEventType = "usage.recorded"
	EventBudgetExceeded          RunEventType = "budget.exceeded"
)

type ContextManifestEntry struct {
	Source           string `json:"source"`
	ReferenceID      string `json:"reference_id"`
	CitationSourceID string `json:"citation_source_id,omitempty"`
	Role             string `json:"role,omitempty"`
	Selected         bool   `json:"selected"`
	Reason           string `json:"reason"`
	Transformation   string `json:"transformation,omitempty"`
	EstimatedTokens  int    `json:"estimated_tokens"`
	OriginalBytes    int    `json:"original_bytes"`
	IncludedBytes    int    `json:"included_bytes"`
}

type ContextManifest struct {
	ID                   string                 `json:"id"`
	ModelCallID          string                 `json:"model_call_id"`
	RunID                string                 `json:"run_id"`
	StageID              string                 `json:"stage_id,omitempty"`
	TurnID               string                 `json:"turn_id"`
	Model                string                 `json:"model"`
	AssemblerVersion     string                 `json:"assembler_version"`
	ContextWindowTokens  int                    `json:"context_window_tokens"`
	OutputReserveTokens  int                    `json:"output_reserve_tokens"`
	SafetyMarginTokens   int                    `json:"safety_margin_tokens"`
	InputBudgetTokens    int                    `json:"input_budget_tokens"`
	EstimatedInputTokens int                    `json:"estimated_input_tokens"`
	ExcludedTokens       int                    `json:"excluded_tokens"`
	PrefixHash           string                 `json:"prefix_hash"`
	CompactionID         string                 `json:"compaction_id,omitempty"`
	Entries              []ContextManifestEntry `json:"entries"`
	CreatedAt            time.Time              `json:"created_at"`
}

const CurrentRunEventSchemaVersion = 1

type RunEvent struct {
	ID             string         `json:"id"`
	Type           RunEventType   `json:"type"`
	SchemaVersion  int            `json:"schema_version"`
	Sequence       int64          `json:"sequence"`
	ConversationID string         `json:"conversation_id,omitempty"`
	RunID          string         `json:"run_id"`
	StageID        string         `json:"stage_id,omitempty"`
	TurnID         string         `json:"turn_id,omitempty"`
	ParentEventID  string         `json:"parent_event_id,omitempty"`
	Payload        map[string]any `json:"payload"`
	Timestamp      time.Time      `json:"timestamp"`
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

type RunUsagePurpose string

const (
	UsagePurposePrimary    RunUsagePurpose = "primary"
	UsagePurposeRouter     RunUsagePurpose = "router"
	UsagePurposeCompaction RunUsagePurpose = "compaction"
)

type RunUsageEntryKind string

const (
	UsageModelReservation RunUsageEntryKind = "model.reservation"
	UsageModelSettlement  RunUsageEntryKind = "model.settlement"
	UsageToolExecution    RunUsageEntryKind = "tool.execution"
)

// RunUsageEntry is append-only. A model settlement stores absolute provider
// usage and supersedes the estimate for the same operation when totals are built.
type RunUsageEntry struct {
	ID                  string            `json:"id"`
	RunID               string            `json:"run_id"`
	OperationID         string            `json:"operation_id"`
	StageID             string            `json:"stage_id,omitempty"`
	TurnID              string            `json:"turn_id,omitempty"`
	Kind                RunUsageEntryKind `json:"kind"`
	Purpose             RunUsagePurpose   `json:"purpose"`
	Model               string            `json:"model,omitempty"`
	ToolName            string            `json:"tool_name,omitempty"`
	ModelCalls          int               `json:"model_calls,omitempty"`
	ToolCalls           int               `json:"tool_calls,omitempty"`
	PromptTokens        int               `json:"prompt_tokens,omitempty"`
	CompletionTokens    int               `json:"completion_tokens,omitempty"`
	TotalTokens         int               `json:"total_tokens,omitempty"`
	EstimatedCostMicros int64             `json:"estimated_cost_micros,omitempty"`
	Estimated           bool              `json:"estimated,omitempty"`
	Timestamp           time.Time         `json:"timestamp"`
}

type RunUsageTotals struct {
	ModelCalls          int   `json:"model_calls"`
	ToolCalls           int   `json:"tool_calls"`
	PromptTokens        int   `json:"prompt_tokens"`
	CompletionTokens    int   `json:"completion_tokens"`
	TotalTokens         int   `json:"total_tokens"`
	EstimatedCostMicros int64 `json:"estimated_cost_micros"`
	OpenReservations    int   `json:"open_reservations"`
}

type RunUsageLedger struct {
	RunID     string           `json:"run_id"`
	Budget    RuntimeRunBudget `json:"budget"`
	Totals    RunUsageTotals   `json:"totals"`
	Entries   []RunUsageEntry  `json:"entries"`
	UpdatedAt *time.Time       `json:"updated_at,omitempty"`
}

type RunReplay struct {
	Run                   Run                    `json:"run"`
	RuntimeSnapshot       *RuntimeSnapshot       `json:"runtime_snapshot,omitempty"`
	Conversation          Conversation           `json:"conversation"`
	Messages              []Message              `json:"messages"`
	Steps                 []CollaborationStep    `json:"steps"`
	Summary               RunTraceSummary        `json:"summary"`
	UsageLedger           RunUsageLedger         `json:"usage_ledger"`
	RunEvents             []RunEvent             `json:"run_events"`
	VerificationEvidence  []VerificationEvidence `json:"verification_evidence"`
	VerificationArtifacts []VerificationArtifact `json:"verification_artifacts"`
}

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
	Source  string `json:"source"`
	EventID string `json:"event_id,omitempty"`
	StepID  string `json:"step_id,omitempty"`
	Message string `json:"message"`
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
