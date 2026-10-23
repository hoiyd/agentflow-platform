package domain

import (
	"encoding/json"
	"time"
)

// MCPConnection holds persisted connection intent and validated discovery, not
// a live session. Ciphertext and remote definitions are never API fields.
type MCPConnection struct {
	ID                   string              `json:"id"`
	WorkspaceID          string              `json:"workspace_id"`
	Name                 string              `json:"name"`
	URL                  string              `json:"url"`
	AuthMode             string              `json:"auth_mode"`
	Enabled              bool                `json:"enabled"`
	ReadOnlyTools        []string            `json:"read_only_tools"`
	Revision             int64               `json:"revision"`
	Status               string              `json:"status"`
	CredentialCiphertext string              `json:"-"`
	Discovery            []MCPToolDefinition `json:"-"`
	CreatedAt            time.Time           `json:"created_at"`
	UpdatedAt            time.Time           `json:"updated_at"`
}

type MCPToolDefinition struct {
	ProtocolVersion   string          `json:"protocol_version"`
	Definition        json.RawMessage `json:"definition"`
	UnavailableReason string          `json:"unavailable_reason,omitempty"`
}
