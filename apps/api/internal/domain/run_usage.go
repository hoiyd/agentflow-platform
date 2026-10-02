package domain

import (
	"errors"
	"time"
)

// Breakdown counters are subsets of prompt/completion, never additional tokens.
// nil means unknown; a non-nil zero means the provider explicitly reported zero.
type UsageBreakdown struct {
	CachedInputTokens *int   `json:"cached_input_tokens,omitempty"`
	ReasoningTokens   *int   `json:"reasoning_tokens,omitempty"`
	Source            string `json:"source"`
}

func (b *UsageBreakdown) Validate(prompt, completion int) error {
	if b == nil {
		return nil
	}
	if b.Source == "invalid_details" && b.CachedInputTokens == nil && b.ReasoningTokens == nil {
		return nil
	}
	if b.Source != "openai_details" && b.Source != "deepseek_cache" {
		return errors.New("invalid usage breakdown source")
	}
	if b.CachedInputTokens != nil && (*b.CachedInputTokens < 0 || *b.CachedInputTokens > prompt) {
		return errors.New("cached input exceeds prompt usage")
	}
	if b.ReasoningTokens != nil && (*b.ReasoningTokens < 0 || *b.ReasoningTokens > completion) {
		return errors.New("reasoning exceeds completion usage")
	}
	return nil
}

type UsageCostDetails struct {
	Status               string            `json:"status"`
	Reason               string            `json:"reason"`
	Pricing              ModelRoutePricing `json:"pricing"`
	CacheDiscountApplied bool              `json:"cache_discount_applied"`
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
	Breakdown           *UsageBreakdown   `json:"breakdown,omitempty"`
	CostDetails         *UsageCostDetails `json:"cost_details,omitempty"`
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
	CostUnknownEntries  int   `json:"cost_unknown_entries"`
}

type RunUsageLedger struct {
	RunID     string           `json:"run_id"`
	Budget    RuntimeRunBudget `json:"budget"`
	Totals    RunUsageTotals   `json:"totals"`
	Entries   []RunUsageEntry  `json:"entries"`
	UpdatedAt *time.Time       `json:"updated_at,omitempty"`
}
