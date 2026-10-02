package domain

import "time"

// Workspace is an owned entity; its ID represents a generated BIGINT, not a name.
// Encoding it as a string keeps Go/browser identifiers exact above 2^53.
type Workspace struct {
	ID          string     `json:"id"`
	OwnerUserID string     `json:"owner_user_id"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Status      string     `json:"status"`
	IsDefault   bool       `json:"is_default"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	DeletedAt   *time.Time `json:"deleted_at,omitempty"`
}

type WorkspaceUpdate struct {
	Name                   *string `json:"name,omitempty"`
	Description            *string `json:"description,omitempty"`
	Status                 *string `json:"status,omitempty"`
	MakeDefault            bool    `json:"make_default,omitempty"`
	ReplacementWorkspaceID string  `json:"replacement_workspace_id,omitempty"`
}
