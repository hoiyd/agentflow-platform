package domain

import "time"

type ContextManifestEntry struct {
	Source           string   `json:"source"`
	ReferenceID      string   `json:"reference_id"`
	CitationSourceID string   `json:"citation_source_id,omitempty"`
	Role             string   `json:"role,omitempty"`
	Selected         bool     `json:"selected"`
	Reason           string   `json:"reason"`
	Transformation   string   `json:"transformation,omitempty"`
	PolicyVersion    string   `json:"policy_version,omitempty"`
	EstimatedTokens  int      `json:"estimated_tokens"`
	OriginalBytes    int      `json:"original_bytes"`
	IncludedBytes    int      `json:"included_bytes"`
	ArtifactIDs      []string `json:"artifact_ids,omitempty"`
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
