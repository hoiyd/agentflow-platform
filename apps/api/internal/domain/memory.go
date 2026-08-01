package domain

import "time"

type MemoryCandidateStatus string

const (
	MemoryCandidateAccepted MemoryCandidateStatus = "accepted"
	MemoryCandidateRejected MemoryCandidateStatus = "rejected"
)

// MemoryCandidate is an auditable proposal derived from a source message.
// Raw messages remain authoritative; only accepted candidates become Memory.
type MemoryCandidate struct {
	ID               string                `json:"id"`
	ConversationID   string                `json:"conversation_id,omitempty"`
	RunID            string                `json:"run_id,omitempty"`
	SourceMessageID  string                `json:"source_message_id"`
	SourceRole       string                `json:"source_role"`
	Kind             string                `json:"kind"`
	Content          string                `json:"content"`
	Status           MemoryCandidateStatus `json:"status"`
	ExtractionReason string                `json:"extraction_reason"`
	PolicyReason     string                `json:"policy_reason"`
	Confidence       float64               `json:"confidence"`
	CreatedAt        time.Time             `json:"created_at"`
}

type Memory struct {
	ID              string         `json:"id"`
	WorkspaceID     string         `json:"workspace_id,omitempty"`
	UserID          string         `json:"user_id,omitempty"`
	ProjectID       string         `json:"project_id,omitempty"`
	ConversationID  string         `json:"conversation_id,omitempty"`
	RunID           string         `json:"run_id,omitempty"`
	SourceMessageID string         `json:"source_message_id,omitempty"`
	Kind            string         `json:"kind"`
	Content         string         `json:"content"`
	Metadata        map[string]any `json:"metadata"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
}

type MemoryEmbedding struct {
	MemoryID   string    `json:"memory_id"`
	Provider   string    `json:"provider"`
	Model      string    `json:"model"`
	Dimensions int       `json:"dimensions"`
	Embedding  []float64 `json:"-"`
	CreatedAt  time.Time `json:"created_at"`
}

type MemorySearch struct {
	Query             string            `json:"query"`
	Embedding         []float64         `json:"-"`
	EmbeddingProvider string            `json:"-"`
	EmbeddingModel    string            `json:"-"`
	WorkspaceID       string            `json:"workspace_id,omitempty"`
	UserID            string            `json:"user_id,omitempty"`
	ProjectID         string            `json:"project_id,omitempty"`
	Metadata          map[string]string `json:"metadata,omitempty"`
	Limit             int               `json:"limit,omitempty"`
}

type RetrievedMemory struct {
	Memory       Memory  `json:"memory"`
	Similarity   float64 `json:"similarity"`
	RecencyBoost float64 `json:"recency_boost"`
	Score        float64 `json:"score"`
}
