package domain

import "time"

const CurrentToolArtifactSchemaVersion = 1

// ToolArtifact is immutable metadata for a redacted Tool result stored outside
// model Context. Physical storage locations are deliberately not exposed.
type ToolArtifact struct {
	ID                 string     `json:"id"`
	SchemaVersion      int        `json:"schema_version"`
	RunID              string     `json:"run_id"`
	StageID            string     `json:"stage_id,omitempty"`
	TurnID             string     `json:"turn_id,omitempty"`
	ToolCallID         string     `json:"tool_call_id"`
	ToolName           string     `json:"tool_name"`
	DefinitionRevision string     `json:"definition_revision,omitempty"`
	MediaType          string     `json:"media_type"`
	ContentHash        string     `json:"content_hash"`
	OriginalByteSize   int        `json:"original_byte_size"`
	StoredByteSize     int        `json:"stored_byte_size"`
	Redacted           bool       `json:"redacted"`
	RedactionStrategy  string     `json:"redaction_strategy,omitempty"`
	RedactionCount     int        `json:"redaction_count"`
	CreatedAt          time.Time  `json:"created_at"`
	ExpiresAt          *time.Time `json:"expires_at,omitempty"`
	Expired            bool       `json:"expired,omitempty"`
}

// ToolArtifactReference is the bounded, model-visible locator returned in a
// Tool result. Callers must use the retrieval hint instead of treating ID as a
// local path or storage key.
type ToolArtifactReference struct {
	ID            string `json:"id"`
	MediaType     string `json:"media_type"`
	ContentHash   string `json:"content_hash"`
	ByteSize      int    `json:"byte_size"`
	RetrievalHint string `json:"retrieval_hint"`
}

func (a ToolArtifact) Reference() ToolArtifactReference {
	return ToolArtifactReference{
		ID: a.ID, MediaType: a.MediaType, ContentHash: a.ContentHash, ByteSize: a.StoredByteSize,
		RetrievalHint: "Use artifact_read with this artifact ID for bounded content, or artifact_search to locate text.",
	}
}

type ToolArtifactRead struct {
	Artifact   ToolArtifact `json:"artifact"`
	Offset     int          `json:"offset"`
	Content    string       `json:"content"`
	NextOffset int          `json:"next_offset"`
	Complete   bool         `json:"complete"`
}

type ToolArtifactSearchMatch struct {
	Offset  int    `json:"offset"`
	Preview string `json:"preview"`
}

type ToolArtifactSearchResult struct {
	Artifact     ToolArtifact              `json:"artifact"`
	Query        string                    `json:"query"`
	Matches      []ToolArtifactSearchMatch `json:"matches"`
	ScannedBytes int                       `json:"scanned_bytes"`
	Truncated    bool                      `json:"truncated"`
}
