package contexteval

import (
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/evaluation/evalreport"
)

type LiveOptions struct {
	DatasetPath         string        `json:"-"`
	Trials              int           `json:"trials"`
	MaxModelCalls       int           `json:"max_model_calls"`
	MaxTotalTokens      int           `json:"max_total_tokens"`
	Timeout             time.Duration `json:"sample_timeout_ns"`
	ContextWindowTokens int           `json:"context_window_tokens"`
	OutputReserveTokens int           `json:"output_reserve_tokens"`
	SummaryMaxTokens    int           `json:"summary_max_tokens"`
	Revision            string        `json:"-"`
	EvidenceMode        string        `json:"evidence_mode"`
}

type LiveCheck struct {
	Passed           bool     `json:"passed"`
	ValuesPassed     bool     `json:"fact_values_passed"`
	ProvenancePassed bool     `json:"fact_provenance_passed"`
	Findings         []string `json:"findings"`
}

type LivePhase struct {
	Name       string                `json:"name"`
	TurnID     string                `json:"turn_id"`
	DurationMS int64                 `json:"duration_ms"`
	Usage      domain.RunUsageTotals `json:"usage"`
	ErrorCode  string                `json:"error_code,omitempty"`
	Output     string                `json:"output,omitempty"`
}

type LiveEvent struct {
	ID          string              `json:"id"`
	Type        domain.RunEventType `json:"type"`
	Sequence    int64               `json:"sequence"`
	PayloadHash string              `json:"payload_hash"`
}

type LiveSample struct {
	CaseID           string                    `json:"case_id"`
	Trial            int                       `json:"trial"`
	Arm              string                    `json:"arm"`
	Status           string                    `json:"status"`
	Reason           string                    `json:"reason,omitempty"`
	RunID            string                    `json:"run_id"`
	ConversationID   string                    `json:"conversation_id"`
	BoundaryHash     string                    `json:"boundary_hash"`
	SourceMessages   map[string]string         `json:"source_messages"`
	InputHash        string                    `json:"input_hash"`
	StateHash        string                    `json:"state_hash"`
	TaskState        domain.TaskStateRevision  `json:"task_state"`
	Artifact         domain.ToolArtifact       `json:"artifact"`
	Compaction       *domain.ContextCompaction `json:"compaction,omitempty"`
	SummaryHash      string                    `json:"summary_hash,omitempty"`
	Fallback         bool                      `json:"raw_history_fallback"`
	HistoryUnchanged bool                      `json:"history_unchanged"`
	Phases           []LivePhase               `json:"phases"`
	Fidelity         LiveCheck                 `json:"fidelity"`
	Outcome          LiveCheck                 `json:"outcome"`
	Ledger           domain.RunUsageLedger     `json:"usage_ledger"`
	PhysicalRequests int                       `json:"physical_requests"`
	Manifests        []domain.ContextManifest  `json:"manifests"`
	Events           []LiveEvent               `json:"events"`
	MaterialReads    []LiveMaterialRead        `json:"material_reads"`
}

type LiveSummary struct {
	Samples      int `json:"samples"`
	Verified     int `json:"verified"`
	NotEvaluated int `json:"not_evaluated"`
	Pairs        int `json:"pairs"`
	Comparable   int `json:"comparable_pairs"`
	Improved     int `json:"improved"`
	Regressed    int `json:"regressed"`
	Unchanged    int `json:"unchanged"`
}

type LiveReport struct {
	evalreport.Identity
	SchemaVersion    string                       `json:"schema_version"`
	Config           LiveOptions                  `json:"config"`
	Model            string                       `json:"model"`
	Provider         string                       `json:"provider"`
	Sampling         domain.GenerationPolicy      `json:"sampling"`
	Assembly         domain.ContextAssemblyConfig `json:"context_assembly"`
	Algorithm        string                       `json:"compaction_algorithm"`
	SensorVersion    string                       `json:"sensor_version"`
	PromptHash       string                       `json:"prompt_hash"`
	ToolContractHash string                       `json:"tool_contract_hash"`
	Dataset          liveDataset                  `json:"dataset"`
	CostMicros       *int64                       `json:"cost_micros"`
	CostSource       string                       `json:"cost_source"`
	Samples          []LiveSample                 `json:"samples"`
	Pairs            []LivePair                   `json:"pairs"`
	Summary          LiveSummary                  `json:"summary"`
	Usage            domain.RunUsageTotals        `json:"usage"`
	PhysicalRequests int                          `json:"physical_requests"`
	Gate             evalreport.Gate              `json:"gate"`
}

// Positive deltas mean the candidate consumed more than the full-history arm.
// Summary and probe costs must not be hidden inside continuation savings.
type LivePair struct {
	CaseID                 string `json:"case_id"`
	Trial                  int    `json:"trial"`
	BaselineRunID          string `json:"baseline_run_id"`
	CandidateRunID         string `json:"candidate_run_id"`
	Reason                 string `json:"incomparable_reason,omitempty"`
	FidelityRegressed      bool   `json:"fidelity_regressed"`
	OutcomeRegressed       bool   `json:"outcome_regressed"`
	CompactionCalls        int    `json:"compaction_calls"`
	CompactionTokens       int    `json:"compaction_tokens"`
	CompactionMS           int64  `json:"compaction_ms"`
	ContinuationTokenDelta *int   `json:"continuation_token_delta"`
	TotalTokenDelta        *int   `json:"total_token_delta"`
	ContinuationMSDelta    *int64 `json:"continuation_ms_delta"`
}

type LiveMaterialRead struct {
	EventID     string `json:"event_id"`
	CallID      string `json:"call_id"`
	ArtifactID  string `json:"artifact_id"`
	ContentHash string `json:"returned_content_hash"`
	Offset      int    `json:"offset"`
	Bytes       int    `json:"bytes"`
	Complete    bool   `json:"complete"`
}
