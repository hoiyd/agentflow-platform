package domain

import "time"

const InterruptedWorkerReason = "worker_interrupted"

// InterruptedRunRepair is an optimistic, atomic mutation used during cold
// startup. Stores must re-check both the stale heartbeat and event cursor before
// appending terminal events and changing the Run status.
type InterruptedRunRepair struct {
	RunID               string
	StaleBefore         time.Time
	ExpectedEventCursor int64
	TerminalEvents      []RunEvent
	ErrorMessage        string
}

type InterruptedRunRepairResult struct {
	Run            Run
	AppendedEvents []RunEvent
	Applied        bool
}

type StageCheckpointStatus string

const (
	CheckpointPrepared            StageCheckpointStatus = "prepared"
	CheckpointExecuting           StageCheckpointStatus = "executing"
	CheckpointCommitted           StageCheckpointStatus = "committed"
	CheckpointCompensated         StageCheckpointStatus = "compensated"
	CheckpointNeedsReconciliation StageCheckpointStatus = "needs_reconciliation"
)

// StageCheckpoint is the durable internal-state boundary for one Stage. It
// stores hashes and references, not credentials or a second copy of Run input.
type StageCheckpoint struct {
	ID                  string                `json:"id"`
	Provider            string                `json:"provider"`
	RunID               string                `json:"run_id"`
	ConversationID      string                `json:"conversation_id"`
	StageID             string                `json:"stage_id"`
	Status              StageCheckpointStatus `json:"status"`
	InputHash           string                `json:"input_hash"`
	OutputHash          string                `json:"output_hash,omitempty"`
	RuntimeSnapshotHash string                `json:"runtime_snapshot_hash"`
	ToolDefinitionsHash string                `json:"tool_definitions_hash"`
	EventCursor         int64                 `json:"event_cursor"`
	Error               string                `json:"error,omitempty"`
	CreatedAt           time.Time             `json:"created_at"`
	UpdatedAt           time.Time             `json:"updated_at"`
}

type ToolEffectStatus string

const (
	ToolEffectPrepared            ToolEffectStatus = "prepared"
	ToolEffectExecuting           ToolEffectStatus = "executing"
	ToolEffectCommitted           ToolEffectStatus = "committed"
	ToolEffectCompensated         ToolEffectStatus = "compensated"
	ToolEffectNeedsReconciliation ToolEffectStatus = "needs_reconciliation"
)

// ToolEffectRecord is an idempotency journal for tools that declare external
// side effects. A committed result may be replayed without invoking the tool.
type ToolEffectRecord struct {
	IdempotencyKey string           `json:"idempotency_key"`
	RunID          string           `json:"run_id"`
	StageID        string           `json:"stage_id"`
	TurnID         string           `json:"turn_id,omitempty"`
	ToolCallID     string           `json:"tool_call_id"`
	ToolName       string           `json:"tool_name"`
	RequestHash    string           `json:"request_hash"`
	Status         ToolEffectStatus `json:"status"`
	Result         []byte           `json:"result,omitempty"`
	Error          string           `json:"error,omitempty"`
	CreatedAt      time.Time        `json:"created_at"`
	UpdatedAt      time.Time        `json:"updated_at"`
}

// ToolEffectSummary exposes recovery state without duplicating a potentially
// sensitive tool result in Replay. The result remains available to the
// executor for idempotent replay and is already subject to Tool tracing policy.
type ToolEffectSummary struct {
	IdempotencyKey string           `json:"idempotency_key"`
	RunID          string           `json:"run_id"`
	StageID        string           `json:"stage_id"`
	TurnID         string           `json:"turn_id,omitempty"`
	ToolCallID     string           `json:"tool_call_id"`
	ToolName       string           `json:"tool_name"`
	RequestHash    string           `json:"request_hash"`
	Status         ToolEffectStatus `json:"status"`
	HasResult      bool             `json:"has_result"`
	Error          string           `json:"error,omitempty"`
	CreatedAt      time.Time        `json:"created_at"`
	UpdatedAt      time.Time        `json:"updated_at"`
}

func SummarizeToolEffects(records []ToolEffectRecord) []ToolEffectSummary {
	items := make([]ToolEffectSummary, 0, len(records))
	for _, record := range records {
		items = append(items, ToolEffectSummary{
			IdempotencyKey: record.IdempotencyKey, RunID: record.RunID, StageID: record.StageID,
			TurnID: record.TurnID, ToolCallID: record.ToolCallID, ToolName: record.ToolName,
			RequestHash: record.RequestHash, Status: record.Status, HasResult: len(record.Result) > 0,
			Error: record.Error, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt,
		})
	}
	return items
}
