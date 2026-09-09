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
	WorkspaceID      string                `json:"workspace_id"`
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
	Version         int64          `json:"version"`
	DeletedAt       *time.Time     `json:"deleted_at,omitempty"`
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

// MemoryMutation replaces a revision of one stable Memory identity. Actor is
// audit attribution, not authenticated identity or authorization.
type MemoryMutation struct {
	OperationID     string `json:"operation_id"`
	ExpectedVersion int64  `json:"expected_version"`
	Action          string `json:"action"`
	Content         string `json:"content,omitempty"`
	Actor           string `json:"actor"`
	Reason          string `json:"reason"`
}

// No prior content or embedding is retained in the mutation audit.
type MemoryChange struct {
	WorkspaceID     string    `json:"workspace_id"`
	MemoryID        string    `json:"memory_id"`
	OperationID     string    `json:"operation_id"`
	CommandHash     string    `json:"command_hash"`
	Action          string    `json:"action"`
	PreviousVersion int64     `json:"previous_version"`
	Version         int64     `json:"version"`
	SourceMessageID string    `json:"source_message_id,omitempty"`
	Actor           string    `json:"actor"`
	Reason          string    `json:"reason"`
	CreatedAt       time.Time `json:"created_at"`
}

type MemoryDetail struct {
	Memory  Memory         `json:"memory"`
	Changes []MemoryChange `json:"changes"`
}

type MemoryMutationResult struct {
	Memory  Memory       `json:"memory"`
	Change  MemoryChange `json:"change"`
	Applied bool         `json:"applied"`
}

type MemoryEmbedding struct {
	MemoryID   string `json:"memory_id"`
	Provider   string `json:"provider"`
	Model      string `json:"model"`
	Dimensions int    `json:"dimensions"`
	// Vectors belong to the internal embedding record; API responses use Memory.
	Embedding []float64 `json:"embedding"`
	CreatedAt time.Time `json:"created_at"`
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
