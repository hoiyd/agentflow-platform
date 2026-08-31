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

const (
	LegacyRuntimeSnapshotVersion     = 1
	ContextRuntimeSnapshotVersion    = 2
	CompactionRuntimeSnapshotVersion = 3
	RunBudgetRuntimeSnapshotVersion  = 4
	UnifiedExecutionSnapshotVersion  = 5
	SessionHistorySnapshotVersion    = 6
	RecoveryRuntimeSnapshotVersion   = 7
	TaskStateRuntimeSnapshotVersion  = 8
	DelegationRuntimeSnapshotVersion = 9
	PreviousRuntimeSnapshotVersion   = TaskStateRuntimeSnapshotVersion
	CurrentRuntimeSnapshotVersion    = DelegationRuntimeSnapshotVersion
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
	ChildRunPolicy   *RuntimeChildRunPolicy `json:"child_run_policy,omitempty"`
	Delegation       *RuntimeDelegation     `json:"delegation,omitempty"`
	CreatedAt        time.Time              `json:"created_at"`
}

// RuntimeChildRunPolicy is frozen with a parent Multi-Agent Run. Process-level
// concurrency remains live deployment policy, while semantic authority and
// resource bounds cannot change while the parent waits for plan approval.
type RuntimeChildRunPolicy struct {
	MaxDepth              int              `json:"max_depth"`
	TimeoutMS             int64            `json:"timeout_ms"`
	SummaryMaxChars       int              `json:"summary_max_chars"`
	AgentDefinitionSource string           `json:"agent_definition_source"`
	RunBudget             RuntimeRunBudget `json:"run_budget"`
}

// RuntimeDelegation freezes the authority and isolation boundary of a child
// Run. A child receives only its explicit task and selected agent snapshot; it
// does not implicitly inherit parent conversation history or durable task state.
type RuntimeDelegation struct {
	DelegationID    string `json:"delegation_id"`
	ParentRunID     string `json:"parent_run_id"`
	ParentTurnID    string `json:"parent_turn_id"`
	ParentStageID   string `json:"parent_stage_id,omitempty"`
	Depth           int    `json:"depth"`
	IsolatedContext bool   `json:"isolated_context"`
	TimeoutMS       int64  `json:"timeout_ms"`
	SummaryMaxChars int    `json:"summary_max_chars"`
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
	HistoryRetrievalEnabled    bool    `json:"history_retrieval_enabled"`
	HistoryRetrievalMaxResults int     `json:"history_retrieval_max_results"`
	HistoryRetrievalMaxChars   int     `json:"history_retrieval_max_characters"`
	HistoryRetrievalMaxTokens  int     `json:"history_retrieval_max_tokens"`
	HistoryRetrievalWindow     int     `json:"history_retrieval_window"`
	CompactionMode             string  `json:"compaction_mode"`
	CompactionSoftThreshold    float64 `json:"compaction_soft_threshold"`
	CompactionHardThreshold    float64 `json:"compaction_hard_threshold"`
	CompactionRecentTokens     int     `json:"compaction_recent_tokens"`
	CompactionSummaryMaxTokens int     `json:"compaction_summary_max_tokens"`
	CompactionTimeoutMS        int64   `json:"compaction_timeout_ms"`
}

type ContextCompaction struct {
	ID                   string                  `json:"id"`
	ConversationID       string                  `json:"conversation_id"`
	RunID                string                  `json:"run_id"`
	Trigger              string                  `json:"trigger"`
	Status               ContextCompactionStatus `json:"status"`
	Generation           int64                   `json:"generation"`
	PreviousCompactionID string                  `json:"previous_compaction_id,omitempty"`
	ReplacementSummaryID string                  `json:"replacement_summary_id"`
	Summary              string                  `json:"summary"`
	SourceMessageIDs     []string                `json:"source_message_ids"`
	SourceEventIDs       []string                `json:"source_event_ids"`
	ShadowedMessageRange ContextShadowedRange    `json:"shadowed_message_range"`
	SourceHash           string                  `json:"source_hash"`
	BeforeTokens         int                     `json:"before_tokens"`
	AfterTokens          int                     `json:"after_tokens"`
	TargetSummaryTokens  int                     `json:"target_summary_tokens"`
	ReductionRatio       float64                 `json:"reduction_ratio"`
	ConsecutiveLowYield  int                     `json:"consecutive_low_yield"`
	SummaryModel         string                  `json:"summary_model"`
	AlgorithmVersion     string                  `json:"algorithm_version"`
	SurfaceReplacedAt    *time.Time              `json:"surface_replaced_at,omitempty"`
	CreatedAt            time.Time               `json:"created_at"`
}

type ContextCompactionStatus string

const (
	// Completed means the summary surface and terminal event were committed atomically.
	ContextCompactionCompleted ContextCompactionStatus = "completed"
)

// ContextShadowedRange identifies the exact original message interval replaced
// by a compaction summary in the assembled context. Original messages remain
// durable and retrievable.
type ContextShadowedRange struct {
	FirstMessageID string `json:"first_message_id,omitempty"`
	LastMessageID  string `json:"last_message_id,omitempty"`
	MessageCount   int    `json:"message_count"`
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
	SideEffect  string         `json:"side_effect,omitempty"`
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
	EventModelRequestPrepared    RunEventType = "model.request_prepared"
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
	EventHistorySearchStarted    RunEventType = "session_history.search_started"
	EventHistorySearchCompleted  RunEventType = "session_history.search_completed"
	EventHistorySearchFailed     RunEventType = "session_history.search_failed"
	EventTaskStateUpdated        RunEventType = "task_state.updated"
	EventCitationResolved        RunEventType = "citation.resolved"
	EventMemoryCandidateProposed RunEventType = "memory.candidate.proposed"
	EventMemoryCandidateAccepted RunEventType = "memory.candidate.accepted"
	EventMemoryCandidateRejected RunEventType = "memory.candidate.rejected"
	EventMemoryCandidateFailed   RunEventType = "memory.candidate.failed"
	EventMemoryRecallFailed      RunEventType = "memory.recall.failed"
	EventMemorySyncRequested     RunEventType = "memory.sync.requested"
	EventMemorySyncRejected      RunEventType = "memory.sync.rejected"
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
	EventCheckpointCaptured      RunEventType = "checkpoint.captured"
	EventCheckpointRestored      RunEventType = "checkpoint.restored"
	EventCheckpointStale         RunEventType = "checkpoint.stale"
	EventCompensationStarted     RunEventType = "checkpoint.compensation_started"
	EventCompensationCompleted   RunEventType = "checkpoint.compensation_completed"
	EventCompensationFailed      RunEventType = "checkpoint.compensation_failed"
	EventDelegationCreated       RunEventType = "delegation.created"
	EventDelegationStarted       RunEventType = "delegation.started"
	EventDelegationBlocked       RunEventType = "delegation.blocked"
	EventDelegationCompleted     RunEventType = "delegation.completed"
	EventDelegationFailed        RunEventType = "delegation.failed"
	EventDelegationCanceled      RunEventType = "delegation.canceled"
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
	CompactionGeneration int64                  `json:"compaction_generation,omitempty"`
	Entries              []ContextManifestEntry `json:"entries"`
	CreatedAt            time.Time              `json:"created_at"`
}

const CurrentRunEventSchemaVersion = 1

// RunEvent is the typed execution-event contract. Durable sinks persist
// lifecycle events but may omit stream-only events such as model.delta. Trace,
// Replay, and Episode Report views do not maintain another event history.
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
	VerificationEvidence  []VerificationEvidence `json:"verification_evidence"`
	VerificationArtifacts []VerificationArtifact `json:"verification_artifacts"`
	TaskStateRevisions    []TaskStateRevision    `json:"task_state_revisions"`
	ParentDelegation      *RunDelegation         `json:"parent_delegation,omitempty"`
	ChildDelegations      []RunDelegation        `json:"child_delegations"`
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
