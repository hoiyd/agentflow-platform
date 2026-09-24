package domain

import "time"

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
