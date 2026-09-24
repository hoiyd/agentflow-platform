package domain

import "time"

const (
	LegacyRuntimeSnapshotVersion         = 1
	ContextRuntimeSnapshotVersion        = 2
	CompactionRuntimeSnapshotVersion     = 3
	RunBudgetRuntimeSnapshotVersion      = 4
	UnifiedExecutionSnapshotVersion      = 5
	SessionHistorySnapshotVersion        = 6
	RecoveryRuntimeSnapshotVersion       = 7
	TaskStateRuntimeSnapshotVersion      = 8
	ToolContractRuntimeSnapshotVersion   = 10
	ToolSecurityRuntimeSnapshotVersion   = 11
	ToolProgressRuntimeSnapshotVersion   = 12
	AgentSelectionRuntimeSnapshotVersion = 13
	RoutingHintsRuntimeSnapshotVersion   = 14
	RoutingRequirementsSnapshotVersion   = 15
	ModelRoutingRuntimeSnapshotVersion   = 16
	IndependentEmbeddingSnapshotVersion  = 17
	SamplingRuntimeSnapshotVersion       = 18
	CurrentRuntimeSnapshotVersion        = SamplingRuntimeSnapshotVersion
)

type RuntimeSnapshot struct {
	SchemaVersion   int                    `json:"schema_version"`
	Mode            string                 `json:"mode"`
	Agent           RuntimeAgentSnapshot   `json:"agent"`
	CandidateAgents []RuntimeAgentSnapshot `json:"candidate_agents,omitempty"`
	// LegacyModel is populated only when replaying pre-v17 snapshots.
	LegacyModel        *RuntimeModelSnapshot     `json:"model,omitempty"`
	Embedding          RuntimeEmbeddingSnapshot  `json:"embedding"`
	ModelRouting       ModelRouteCatalogSnapshot `json:"model_routing"`
	Tools              []RuntimeToolSnapshot     `json:"tools"`
	ToolSecurityPolicy ToolSecurityPolicy        `json:"tool_security_policy"`
	ToolProgressGuard  ToolProgressGuardConfig   `json:"tool_progress_guard"`
	ContextAssembly    ContextAssemblyConfig     `json:"context_assembly"`
	RouterMode         string                    `json:"router_mode,omitempty"`
	AutonomousLimits   *RuntimeLimitsSnapshot    `json:"autonomous_limits,omitempty"`
	RunBudget          *RuntimeRunBudget         `json:"run_budget,omitempty"`
	CreatedAt          time.Time                 `json:"created_at"`
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
	ID               string            `json:"id"`
	Name             string            `json:"name"`
	Description      string            `json:"description"`
	SystemPrompt     string            `json:"system_prompt"`
	RoutingHints     AgentRoutingHints `json:"routing_hints,omitempty"`
	Tools            []string          `json:"tools"`
	MemoryEnabled    bool              `json:"memory_enabled"`
	RetrievalEnabled bool              `json:"retrieval_enabled"`
	Executor         string            `json:"executor"`
}

type RuntimeModelSnapshot struct {
	Provider            string `json:"provider"`
	BaseURL             string `json:"base_url"`
	Model               string `json:"model"`
	EmbeddingBaseURL    string `json:"embedding_base_url"`
	EmbeddingModel      string `json:"embedding_model"`
	EmbeddingDimensions int    `json:"embedding_dimensions"`
}

// RuntimeEmbeddingSnapshot freezes the single embedding service independently
// from the peer Chat LLM routes.
type RuntimeEmbeddingSnapshot struct {
	Provider   string `json:"provider"`
	BaseURL    string `json:"base_url"`
	Model      string `json:"model"`
	Dimensions int    `json:"dimensions"`
}

// ModelRouteCatalogSnapshot freezes the only routes a resumed Run may use.
// CredentialEnvironment names a process environment variable; its value is
// deliberately resolved at runtime and never persisted.
type ModelRouteCatalogSnapshot struct {
	PolicyRevision  string                 `json:"policy_revision"`
	CatalogRevision string                 `json:"catalog_revision"`
	PinnedRouteID   string                 `json:"pinned_route_id,omitempty"`
	Routes          []ModelRouteDescriptor `json:"routes"`
}

type ModelRouteDescriptor struct {
	ID                    string                 `json:"id"`
	Provider              string                 `json:"provider"`
	Model                 string                 `json:"model"`
	Endpoint              string                 `json:"endpoint"`
	Capabilities          ModelRouteCapabilities `json:"capabilities"`
	GenerationPolicy      *GenerationPolicy      `json:"generation_policy,omitempty"`
	ContextWindowTokens   int                    `json:"context_window_tokens"`
	MaxOutputTokens       int                    `json:"max_output_tokens"`
	Priority              int                    `json:"priority"`
	Pricing               ModelRoutePricing      `json:"pricing"`
	CredentialEnvironment string                 `json:"credential_environment,omitempty"`
	DefinitionRevision    string                 `json:"definition_revision"`
}

type ModelRouteCapabilities struct {
	ToolCalling      bool `json:"tool_calling"`
	StructuredOutput bool `json:"structured_output"`
	Streaming        bool `json:"streaming"`
	Seed             bool `json:"seed,omitempty"`
}

type ModelRoutePricing struct {
	Source                       string `json:"source"`
	InputPerMillionTokensMicros  int64  `json:"input_per_million_tokens_micros"`
	OutputPerMillionTokensMicros int64  `json:"output_per_million_tokens_micros"`
}

type ModelRouteRequirements struct {
	Purpose              string `json:"purpose"`
	ToolCalling          bool   `json:"tool_calling"`
	StructuredOutput     bool   `json:"structured_output"`
	Streaming            bool   `json:"streaming"`
	EstimatedInputTokens int    `json:"estimated_input_tokens"`
	MaxOutputTokens      int    `json:"max_output_tokens"`
}

type ModelRouteCandidateDecision struct {
	RouteID          string   `json:"route_id"`
	Eligible         bool     `json:"eligible"`
	ExclusionReasons []string `json:"exclusion_reasons,omitempty"`
}

type RuntimeToolSnapshot struct {
	Name               string         `json:"name"`
	Description        string         `json:"description"`
	Parameters         map[string]any `json:"parameters"`
	SchemaVersion      string         `json:"schema_version,omitempty"`
	DefinitionRevision string         `json:"definition_revision,omitempty"`
	SideEffect         string         `json:"side_effect,omitempty"`
	Security           ToolCapability `json:"security,omitempty"`
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
