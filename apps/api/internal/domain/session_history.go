package domain

import "time"

const (
	SessionHistorySourceMessage = "message"
	SessionHistorySourceEvent   = "event"
)

// RetrievedSessionHistory is a bounded, read-only projection of one durable
// Message or RunEvent. Reference remains stable even when the content is
// compacted for a model call.
type RetrievedSessionHistory struct {
	Reference     string       `json:"reference"`
	SourceKind    string       `json:"source_kind"`
	MessageID     string       `json:"message_id,omitempty"`
	EventID       string       `json:"event_id,omitempty"`
	RunID         string       `json:"run_id,omitempty"`
	Role          string       `json:"role,omitempty"`
	EventType     RunEventType `json:"event_type,omitempty"`
	Content       string       `json:"content"`
	OriginalBytes int          `json:"original_bytes"`
	MatchReason   string       `json:"match_reason"`
	DirectMatch   bool         `json:"direct_match"`
	Truncated     bool         `json:"truncated,omitempty"`
	CreatedAt     time.Time    `json:"created_at"`
}
