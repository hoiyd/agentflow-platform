package domain

// RunProjection is the canonical, read-only execution model derived from
// durable RunEvents plus the authoritative Run record. "Projection" means a
// deterministic event-derived view here; this value is not independently
// persisted or mutated.
type RunProjection struct {
	RunID              string             `json:"run_id"`
	ConversationID     string             `json:"conversation_id"`
	Status             RunStatus          `json:"status"`
	VerificationStatus VerificationStatus `json:"verification_status"`
	ActiveStageIDs     []string           `json:"active_stage_ids"`
	ActiveTurnIDs      []string           `json:"active_turn_ids"`
	ActiveModelCallIDs []string           `json:"active_model_call_ids"`
	ActiveToolCallIDs  []string           `json:"active_tool_call_ids"`
	Summary            RunTraceSummary    `json:"summary"`
	AsOfSequence       int64              `json:"as_of_sequence"`
}

// UsageProjection exposes the authoritative Usage Ledger at the same durable
// event watermark as the other Run projections.
type UsageProjection struct {
	Ledger       RunUsageLedger `json:"ledger"`
	AsOfSequence int64          `json:"as_of_sequence"`
}

// VerificationProjection is the read-only verification state derived from the
// Run and its immutable Verification Evidence at a durable event watermark.
type VerificationProjection struct {
	Status             VerificationStatus `json:"status"`
	LatestAttempt      int                `json:"latest_attempt"`
	CurrentSubjectHash string             `json:"current_subject_hash,omitempty"`
	EvidenceCount      int                `json:"evidence_count"`
	FreshEvidenceCount int                `json:"fresh_evidence_count"`
	AsOfSequence       int64              `json:"as_of_sequence"`
}

type RuntimeInvariantFailure struct {
	Code     string `json:"code"`
	Owner    string `json:"owner"`
	RunID    string `json:"run_id"`
	EventID  string `json:"event_id,omitempty"`
	Sequence int64  `json:"sequence,omitempty"`
	Message  string `json:"message"`
}

// RunProjectionSnapshot groups independently derived read models at one
// durable event watermark. It is a coherent query response, not a persisted
// runtime snapshot. InvariantFailures is populated at the API boundary because
// some checks also require model-request records that are not part of
// RunReplay.
type RunProjectionSnapshot struct {
	Run               RunProjection             `json:"run"`
	Usage             UsageProjection           `json:"usage"`
	Verification      VerificationProjection    `json:"verification"`
	AsOfSequence      int64                     `json:"as_of_sequence"`
	InvariantFailures []RuntimeInvariantFailure `json:"invariant_failures"`
}
