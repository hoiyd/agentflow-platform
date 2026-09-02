package event

import (
	"fmt"
	"reflect"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/eventcatalog"
)

type EventMetadata struct {
	ConversationID string
	RunID          string
	StageID        string
	TurnID         string
	ParentEventID  string
	Timestamp      time.Time
}

// NewRunEvent validates the event/payload pairing before converting the typed
// payload to the durable map representation.
func NewRunEvent(eventType domain.RunEventType, metadata EventMetadata, payload any) (domain.RunEvent, error) {
	if !payloadSupports(eventType, payload) {
		return domain.RunEvent{}, fmt.Errorf("payload contract does not support event type %s", eventType)
	}
	encoded, err := Payload(payload)
	if err != nil {
		return domain.RunEvent{}, fmt.Errorf("encode %s payload: %w", eventType, err)
	}
	timestamp := metadata.Timestamp
	if timestamp.IsZero() {
		timestamp = time.Now().UTC()
	}
	item := domain.RunEvent{
		Type: eventType, SchemaVersion: domain.CurrentRunEventSchemaVersion,
		ConversationID: metadata.ConversationID, RunID: metadata.RunID,
		StageID: metadata.StageID, TurnID: metadata.TurnID, ParentEventID: metadata.ParentEventID,
		Payload: encoded, Timestamp: timestamp,
	}
	if err := eventcatalog.ValidateEnvelope(item); err != nil {
		return domain.RunEvent{}, err
	}
	return item, nil
}

func payloadSupports(eventType domain.RunEventType, payload any) bool {
	definition, ok := eventcatalog.DefinitionFor(eventType)
	if !ok || payload == nil {
		return false
	}
	if trace, ok := payload.(TracePayload); ok {
		return trace.EventType == eventType
	}
	payloadType := reflect.TypeOf(payload)
	if payloadType.Kind() == reflect.Pointer {
		payloadType = payloadType.Elem()
	}
	return "event."+payloadType.Name() == definition.PayloadSchema
}

// TracePayload is the explicit escape hatch for extensible observability
// metadata. It remains bound to one event type, so it cannot be reused across
// incompatible event families accidentally.
type TracePayload struct {
	EventType domain.RunEventType
	Fields    map[string]any
}

type RunStatusPayload struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

type RunProgressPayload struct {
	Mode              string `json:"mode"`
	Iteration         int    `json:"iteration,omitempty"`
	MaxIterations     int    `json:"max_iterations,omitempty"`
	ElapsedSeconds    int    `json:"elapsed_seconds,omitempty"`
	MaxRuntimeSeconds int    `json:"max_runtime_seconds,omitempty"`
	OutputChars       int    `json:"output_chars,omitempty"`
	MaxOutputChars    int    `json:"max_output_chars,omitempty"`
	ToolCalls         int    `json:"tool_calls,omitempty"`
	MaxToolCalls      int    `json:"max_tool_calls,omitempty"`
	StopReason        string `json:"stop_reason,omitempty"`
}

type StagePayload struct {
	Name      string `json:"name"`
	AgentID   string `json:"agent_id,omitempty"`
	Iteration int    `json:"iteration,omitempty"`
	Input     string `json:"input,omitempty"`
	Output    string `json:"output,omitempty"`
	Error     string `json:"error,omitempty"`
}

type ModelPayload struct {
	Model               string `json:"model,omitempty"`
	Output              string `json:"output,omitempty"`
	OutputChars         int    `json:"output_chars,omitempty"`
	PromptTokens        int    `json:"prompt_tokens,omitempty"`
	CompletionTokens    int    `json:"completion_tokens,omitempty"`
	TotalTokens         int    `json:"total_tokens,omitempty"`
	TokenUsageEstimated bool   `json:"token_usage_estimated,omitempty"`
	DurationMS          int64  `json:"duration_ms,omitempty"`
	Error               string `json:"error,omitempty"`
}

type ModelRequestPreparedPayload struct {
	RecordID               string `json:"record_id"`
	ModelCallID            string `json:"model_call_id"`
	Attempt                int    `json:"attempt"`
	Operation              string `json:"operation"`
	Provider               string `json:"provider"`
	Model                  string `json:"model"`
	ContextManifestID      string `json:"context_manifest_id,omitempty"`
	RuntimeSnapshotHash    string `json:"runtime_snapshot_hash"`
	PayloadHash            string `json:"payload_hash"`
	PayloadBytes           int    `json:"payload_bytes"`
	CaptureMode            string `json:"capture_mode"`
	CaptureReconstructable bool   `json:"capture_reconstructable"`
}

type ContextAssembledPayload struct {
	Manifest domain.ContextManifest `json:"manifest"`
}

type ContextCompactionPayload struct {
	CompactionID         string                      `json:"compaction_id,omitempty"`
	Generation           int64                       `json:"generation,omitempty"`
	PreviousCompactionID string                      `json:"previous_compaction_id,omitempty"`
	ReplacementSummaryID string                      `json:"replacement_summary_id,omitempty"`
	Trigger              string                      `json:"trigger"`
	TriggerKey           string                      `json:"trigger_key,omitempty"`
	Status               string                      `json:"status"`
	SourceMessageIDs     []string                    `json:"source_message_ids,omitempty"`
	SourceEventIDs       []string                    `json:"source_event_ids,omitempty"`
	ShadowedMessageRange domain.ContextShadowedRange `json:"shadowed_message_range,omitempty"`
	BeforeTokens         int                         `json:"before_tokens,omitempty"`
	AfterTokens          int                         `json:"after_tokens,omitempty"`
	TargetSummaryTokens  int                         `json:"target_summary_tokens,omitempty"`
	ReductionRatio       float64                     `json:"reduction_ratio,omitempty"`
	ConsecutiveLowYield  int                         `json:"consecutive_low_yield,omitempty"`
	ObservedPromptTokens int                         `json:"observed_prompt_tokens,omitempty"`
	SummaryModel         string                      `json:"summary_model,omitempty"`
	AlgorithmVersion     string                      `json:"algorithm_version"`
	CooldownReason       string                      `json:"cooldown_reason,omitempty"`
	CooldownUntil        *time.Time                  `json:"cooldown_until,omitempty"`
	RecoveredFromOrphan  bool                        `json:"recovered_from_orphan,omitempty"`
	Error                string                      `json:"error,omitempty"`
}

type ToolPayload struct {
	ToolCallID string `json:"tool_call_id"`
	ToolName   string `json:"tool_name"`
	Arguments  any    `json:"arguments,omitempty"`
	Result     any    `json:"result,omitempty"`
	DurationMS int64  `json:"duration_ms,omitempty"`
	Error      string `json:"error,omitempty"`
}

// ToolPolicyPayload deliberately records only bounded classifications and
// counts. Resource names, network targets, credential scopes, and Secret values
// never enter the durable event.
type ToolPolicyPayload struct {
	ToolCallID           string `json:"tool_call_id,omitempty"`
	ToolName             string `json:"tool_name"`
	PolicyVersion        string `json:"policy_version"`
	RuleID               string `json:"rule_id,omitempty"`
	Action               string `json:"action"`
	Allowed              bool   `json:"allowed"`
	Reason               string `json:"reason"`
	Source               string `json:"source"`
	SideEffectClass      string `json:"side_effect_class"`
	RateClass            string `json:"rate_class"`
	Reversibility        string `json:"reversibility"`
	Visibility           string `json:"visibility"`
	ApprovalMode         string `json:"approval_mode"`
	AuditLevel           string `json:"audit_level"`
	ResourceScopeCount   int    `json:"resource_scope_count"`
	NetworkMode          string `json:"network_mode"`
	NetworkTargetCount   int    `json:"network_target_count"`
	CredentialScopeCount int    `json:"credential_scope_count"`
}

// ToolProgressPayload explains a Guard escalation without retaining Tool
// arguments, result bodies, or raw error messages.
type ToolProgressPayload struct {
	ToolCallID         string `json:"tool_call_id,omitempty"`
	ToolName           string `json:"tool_name"`
	GuardVersion       string `json:"guard_version"`
	Rule               string `json:"rule"`
	Action             string `json:"action"`
	Count              int    `json:"count"`
	Reason             string `json:"reason"`
	SignatureHash      string `json:"signature_hash"`
	OutcomeFingerprint string `json:"outcome_fingerprint"`
}

type ToolArtifactPayload struct {
	ArtifactID    string `json:"artifact_id"`
	ToolCallID    string `json:"tool_call_id,omitempty"`
	ToolName      string `json:"tool_name,omitempty"`
	Operation     string `json:"operation"`
	Offset        int    `json:"offset,omitempty"`
	Limit         int    `json:"limit,omitempty"`
	ReturnedBytes int    `json:"returned_bytes,omitempty"`
	MatchCount    int    `json:"match_count,omitempty"`
	ContentHash   string `json:"content_hash,omitempty"`
	StoredBytes   int    `json:"stored_bytes,omitempty"`
}

type RetrievalPayload struct {
	Query       string `json:"query,omitempty"`
	MemoryCount int    `json:"memory_count"`
	ChunkCount  int    `json:"chunk_count"`
	Error       string `json:"error,omitempty"`
}

type SessionHistorySearchPayload struct {
	Query            string   `json:"query,omitempty"`
	Keywords         []string `json:"keywords,omitempty"`
	ResultCount      int      `json:"result_count"`
	DirectMatchCount int      `json:"direct_match_count"`
	Truncated        bool     `json:"truncated"`
	SourceReferences []string `json:"source_references,omitempty"`
	Error            string   `json:"error,omitempty"`
}

type TaskStatePayload struct {
	RevisionID      string   `json:"revision_id"`
	Version         int64    `json:"version"`
	PreviousVersion int64    `json:"previous_version"`
	OperationTypes  []string `json:"operation_types"`
	ActorType       string   `json:"actor_type"`
}

type MemoryCandidatePayload struct {
	CandidateID      string  `json:"candidate_id,omitempty"`
	SourceMessageID  string  `json:"source_message_id"`
	SourceRole       string  `json:"source_role"`
	Kind             string  `json:"kind,omitempty"`
	Status           string  `json:"status"`
	ExtractionReason string  `json:"extraction_reason,omitempty"`
	PolicyReason     string  `json:"policy_reason,omitempty"`
	Confidence       float64 `json:"confidence,omitempty"`
	Error            string  `json:"error,omitempty"`
}

type UsagePayload struct {
	UsageEntryID        string `json:"usage_entry_id"`
	OperationID         string `json:"operation_id"`
	Kind                string `json:"kind"`
	Purpose             string `json:"purpose"`
	Model               string `json:"model,omitempty"`
	ToolName            string `json:"tool_name,omitempty"`
	ModelCalls          int    `json:"model_calls"`
	ToolCalls           int    `json:"tool_calls"`
	PromptTokens        int    `json:"prompt_tokens"`
	CompletionTokens    int    `json:"completion_tokens"`
	TotalTokens         int    `json:"total_tokens"`
	EstimatedCostMicros int64  `json:"estimated_cost_micros"`
	OpenReservations    int    `json:"open_reservations"`
	UsageEstimated      bool   `json:"usage_estimated"`
}

type BudgetExceededPayload struct {
	Resource    string `json:"resource"`
	Limit       int64  `json:"limit"`
	Used        int64  `json:"used"`
	Requested   int64  `json:"requested"`
	OperationID string `json:"operation_id,omitempty"`
	Purpose     string `json:"purpose,omitempty"`
}
