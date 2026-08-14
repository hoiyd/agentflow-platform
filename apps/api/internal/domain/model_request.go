package domain

import "time"

type ModelRequestCaptureMode string

const (
	ModelRequestCaptureMetadata ModelRequestCaptureMode = "metadata_only"
	ModelRequestCaptureRedacted ModelRequestCaptureMode = "redacted"
	ModelRequestCaptureFull     ModelRequestCaptureMode = "full"
)

// ModelRequestEnvelope is the durable, provider-neutral identity of one
// physical model request attempt. It deliberately excludes credentials and
// raw prompt content.
type ModelRequestEnvelope struct {
	ID                   string         `json:"id"`
	RunID                string         `json:"run_id"`
	ConversationID       string         `json:"conversation_id"`
	StageID              string         `json:"stage_id,omitempty"`
	TurnID               string         `json:"turn_id,omitempty"`
	ModelCallID          string         `json:"model_call_id"`
	Attempt              int            `json:"attempt"`
	Operation            string         `json:"operation"`
	Provider             string         `json:"provider"`
	Model                string         `json:"model"`
	ContextManifestID    string         `json:"context_manifest_id,omitempty"`
	RuntimeSnapshotHash  string         `json:"runtime_snapshot_hash"`
	PayloadHash          string         `json:"payload_hash"`
	PayloadBytes         int            `json:"payload_bytes"`
	Parameters           map[string]any `json:"parameters"`
	SourceTokenBreakdown map[string]int `json:"source_token_breakdown"`
	MessageCount         int            `json:"message_count"`
	ToolCount            int            `json:"tool_count"`
	CreatedAt            time.Time      `json:"created_at"`
}

// ModelRequestCapture stores content according to the configured capture
// policy. Content is canonical JSON when present. PayloadHash on the envelope
// always describes the exact transport bytes, while ContentHash describes the
// stored full or redacted representation.
type ModelRequestCapture struct {
	Mode              ModelRequestCaptureMode `json:"mode"`
	Content           string                  `json:"content,omitempty"`
	ContentHash       string                  `json:"content_hash,omitempty"`
	OriginalBytes     int                     `json:"original_bytes"`
	StoredBytes       int                     `json:"stored_bytes"`
	Redacted          bool                    `json:"redacted"`
	RedactionStrategy string                  `json:"redaction_strategy,omitempty"`
	RedactionCount    int                     `json:"redaction_count"`
	Truncated         bool                    `json:"truncated"`
	Reconstructable   bool                    `json:"reconstructable"`
	ExpiresAt         *time.Time              `json:"expires_at,omitempty"`
	Expired           bool                    `json:"expired"`
}

type ModelRequestRecord struct {
	Envelope ModelRequestEnvelope `json:"envelope"`
	Capture  ModelRequestCapture  `json:"capture"`
}
