package contexteval

import (
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/evaluation/evalreport"
)

type Config struct {
	Strategy            string                       `json:"strategy"`
	Model               string                       `json:"model"`
	AssemblerVersion    string                       `json:"assembler_version"`
	CompactionAlgorithm string                       `json:"compaction_algorithm"`
	Assembly            domain.ContextAssemblyConfig `json:"assembly"`
	FixtureScope        string                       `json:"fixture_scope"`
}

type ModelInputEvidence struct {
	PayloadHash          string         `json:"payload_hash"`
	PayloadBytes         int            `json:"payload_bytes"`
	MessageCount         int            `json:"message_count"`
	SourceTokenBreakdown map[string]int `json:"source_token_breakdown"`
}

type Sample struct {
	CaseID                 string              `json:"case_id"`
	Split                  string              `json:"split"`
	Classification         string              `json:"classification"`
	Scenario               string              `json:"scenario"`
	EvidencePosition       string              `json:"evidence_position,omitempty"`
	Status                 string              `json:"status"`
	Passed                 bool                `json:"passed"`
	RequiredFacts          int                 `json:"required_facts"`
	RetainedFacts          int                 `json:"retained_facts"`
	ForbiddenFacts         int                 `json:"forbidden_facts"`
	ForbiddenLeaks         int                 `json:"forbidden_leaks"`
	InputTokens            int                 `json:"input_tokens"`
	IrrelevantTokens       int                 `json:"irrelevant_tokens"`
	IrrelevantContextRatio float64             `json:"irrelevant_context_ratio"`
	SourceTokenShare       map[string]float64  `json:"source_token_share"`
	PrefixHash             string              `json:"prefix_hash,omitempty"`
	CompactionApplied      bool                `json:"compaction_applied"`
	RecoveryPath           string              `json:"recovery_path,omitempty"`
	ExpectedErrorCode      string              `json:"expected_error_code,omitempty"`
	ErrorCode              string              `json:"error_code,omitempty"`
	FailureReason          string              `json:"failure_reason,omitempty"`
	ModelInput             *ModelInputEvidence `json:"model_input,omitempty"`
}

type Summary struct {
	Samples               int            `json:"samples"`
	Evaluated             int            `json:"evaluated"`
	Passed                int            `json:"passed"`
	Failed                int            `json:"failed"`
	NotEvaluated          int            `json:"not_evaluated"`
	ExpectedErrors        int            `json:"expected_errors"`
	GatingSamples         int            `json:"gating_samples"`
	GatingFailures        int            `json:"gating_failures"`
	RequiredFacts         int            `json:"required_facts"`
	RetainedFacts         int            `json:"retained_facts"`
	RequiredFactRetention float64        `json:"required_fact_retention"`
	ForbiddenFacts        int            `json:"forbidden_facts"`
	ForbiddenLeaks        int            `json:"forbidden_leaks"`
	ForbiddenLeakRate     float64        `json:"forbidden_leak_rate"`
	MeanInputTokens       float64        `json:"mean_input_tokens"`
	MeanIrrelevantRatio   float64        `json:"mean_irrelevant_context_ratio"`
	SourceTokens          map[string]int `json:"source_tokens"`
	PrefixSetHash         string         `json:"prefix_set_hash"`
}

type Comparison struct {
	Mode             string             `json:"mode"`
	Comparable       bool               `json:"comparable"`
	ChangedVariables []string           `json:"changed_variables"`
	Reasons          []string           `json:"reasons"`
	Regressions      []string           `json:"regressions"`
	Deltas           map[string]float64 `json:"deltas"`
}

type Report struct {
	evalreport.Identity
	SchemaVersion string          `json:"schema_version"`
	Config        Config          `json:"config"`
	CostSource    string          `json:"cost_source"`
	Samples       []Sample        `json:"samples"`
	Summary       Summary         `json:"summary"`
	Gate          evalreport.Gate `json:"gate"`
	Comparison    *Comparison     `json:"comparison,omitempty"`
}
