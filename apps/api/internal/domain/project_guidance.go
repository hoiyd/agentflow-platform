package domain

// ProjectContext selects existing relative project files, never a host directory
// or a grant. Operator-owned configuration separately identifies trusted guidance.
type ProjectContext struct {
	Root  string   `json:"root"`
	Paths []string `json:"paths"`
}

type ProjectGuidanceEntry struct {
	Path            string   `json:"path"`
	Patterns        []string `json:"patterns,omitempty"`
	MatchedPaths    []string `json:"matched_paths,omitempty"`
	Reason          string   `json:"reason"`
	Hash            string   `json:"hash,omitempty"`
	Content         string   `json:"content,omitempty"`
	OriginalBytes   int      `json:"original_bytes"`
	EstimatedTokens int      `json:"estimated_tokens"`
}

// Text is bounded and immutable with the Run. Root/config identities are hashes;
// absolute host paths, credentials and arbitrary imported files are not stored.
type ProjectGuidanceSnapshot struct {
	Version        string                 `json:"version"`
	SourceType     string                 `json:"source_type"`
	WorkspaceID    string                 `json:"workspace_id"`
	Selection      ProjectContext         `json:"selection"`
	RootRevision   string                 `json:"root_revision"`
	ConfigRevision string                 `json:"config_revision"`
	Entries        []ProjectGuidanceEntry `json:"entries"`
}
