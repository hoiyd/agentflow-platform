package domain

// WorkspaceToolConfig is an explicit allowlist. Missing entries, including newly
// installed Tools, are denied after the initial configuration has been saved.
type WorkspaceToolConfig struct {
	WorkspaceID  string   `json:"workspace_id"`
	AllowedTools []string `json:"allowed_tools"`
	Revision     int64    `json:"revision"`
}
