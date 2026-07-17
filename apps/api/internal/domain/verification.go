package domain

import "time"

const CurrentCompletionContractVersion = 2

type VerificationStatus string

const (
	// VerificationNotRequired is the default for Runs created without an
	// explicit CompletionContract in the initial chat request.
	VerificationNotRequired VerificationStatus = "not_required"
	VerificationPending     VerificationStatus = "pending"
	VerificationRunning     VerificationStatus = "running"
	VerificationPassed      VerificationStatus = "passed"
	VerificationFailed      VerificationStatus = "failed"
	VerificationBlocked     VerificationStatus = "blocked"
	VerificationStale       VerificationStatus = "stale"
)

type VerificationPolicyMode string

const (
	VerificationAllMustPass VerificationPolicyMode = "all_must_pass"
	VerificationAnyMayPass  VerificationPolicyMode = "any_may_pass"
)

type VerificationFailureAction string

const (
	VerificationFailRun     VerificationFailureAction = "fail"
	VerificationWaitForUser VerificationFailureAction = "waiting_for_user"
)

type VerifierType string

const (
	VerifierCommand         VerifierType = "command"
	VerifierHTTP            VerifierType = "http"
	VerifierJSONSchema      VerifierType = "json_schema"
	VerifierTextConstraints VerifierType = "text_constraints"
	VerifierCitation        VerifierType = "citation"
)

// CompletionContract opts one Run into evidence-gated completion. It must be
// supplied when the Run is created and is frozen before execution starts.
type CompletionContract struct {
	ID          string             `json:"id"`
	Version     int                `json:"version"`
	Hash        string             `json:"hash"`
	SubjectType string             `json:"subject_type"`
	Verifiers   []VerifierSpec     `json:"verifiers"`
	Policy      VerificationPolicy `json:"policy"`
}

type VerificationPolicy struct {
	Mode        VerificationPolicyMode    `json:"mode"`
	MaxAttempts int                       `json:"max_attempts"`
	OnExhausted VerificationFailureAction `json:"on_exhausted"`
}

type VerifierSpec struct {
	ID        string       `json:"id"`
	Type      VerifierType `json:"type"`
	Version   string       `json:"version"`
	Required  bool         `json:"required"`
	TimeoutMS int64        `json:"timeout_ms"`
	// Config is interpreted and normalized by the registered verifier before
	// the full spec is hashed into the frozen CompletionContract.
	Config map[string]any `json:"config"`
}

// VerificationEvidence is immutable. A later subject produces new evidence;
// stale markers reference the evidence they supersede instead of mutating it.
type VerificationEvidence struct {
	ID                   string             `json:"id"`
	RunID                string             `json:"run_id"`
	StageID              string             `json:"stage_id,omitempty"`
	ContractID           string             `json:"contract_id"`
	ContractVersion      int                `json:"contract_version"`
	VerifierID           string             `json:"verifier_id"`
	VerifierType         VerifierType       `json:"verifier_type"`
	VerifierVersion      string             `json:"verifier_version"`
	Attempt              int                `json:"attempt"`
	SubjectHash          string             `json:"subject_hash"`
	SnapshotHash         string             `json:"snapshot_hash"`
	Status               VerificationStatus `json:"status"`
	StartedAt            time.Time          `json:"started_at"`
	CompletedAt          time.Time          `json:"completed_at"`
	DurationMS           int64              `json:"duration_ms"`
	ExitCode             *int               `json:"exit_code,omitempty"`
	Summary              string             `json:"summary"`
	Details              map[string]any     `json:"details"`
	ArtifactIDs          []string           `json:"artifact_ids"`
	SupersedesEvidenceID string             `json:"supersedes_evidence_id,omitempty"`
}

type VerificationArtifact struct {
	ID          string    `json:"id"`
	RunID       string    `json:"run_id"`
	EvidenceID  string    `json:"evidence_id"`
	Kind        string    `json:"kind"`
	MediaType   string    `json:"media_type"`
	Content     string    `json:"content"`
	ContentHash string    `json:"content_hash"`
	ByteSize    int       `json:"byte_size"`
	Truncated   bool      `json:"truncated"`
	CreatedAt   time.Time `json:"created_at"`
}

type VerificationRecord struct {
	Evidence  VerificationEvidence   `json:"evidence"`
	Artifacts []VerificationArtifact `json:"artifacts"`
}
