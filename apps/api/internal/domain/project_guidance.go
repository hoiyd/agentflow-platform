package domain

// ProjectContext selects a logical source directory and relative task files for
// project instructions, not a sub-Workspace, namespace or access grant. Its wire
// names remain stable for existing clients and Run snapshots.
type ProjectContext struct {
	Root  string   `json:"root"`
	Paths []string `json:"paths"`
}

// Discovery exposes logical names and relative file metadata, never host paths
// or guidance bodies. Selection still requires validation when a Run starts.
type ProjectGuidanceCatalog struct {
	Configured bool                     `json:"configured"`
	Projects   []ProjectGuidanceProject `json:"projects"`
}

type ProjectGuidanceProject struct {
	Root      string `json:"root"`
	Entry     string `json:"entry"`
	RuleCount int    `json:"rule_count"`
}

type ProjectFileEntry struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
}

type ProjectFilePage struct {
	Path      string             `json:"path"`
	Entries   []ProjectFileEntry `json:"entries"`
	Truncated bool               `json:"truncated"`
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
