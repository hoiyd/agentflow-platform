package domain

// SkillMetadata advertises a trusted method, not executable authority.
type SkillMetadata struct {
	Name          string   `json:"name"`
	Description   string   `json:"description"`
	Hash          string   `json:"hash"`
	RequiredTools []string `json:"required_tools,omitempty"`
}

// SkillSnapshot contains bounded text frozen with a Run. Resume never reads
// operator directories again; paths identify resources within this package.
type SkillSnapshot struct {
	Name          string          `json:"name"`
	Description   string          `json:"description"`
	Hash          string          `json:"hash"`
	RequiredTools []string        `json:"required_tools,omitempty"`
	Instructions  string          `json:"instructions"`
	Resources     []SkillResource `json:"resources,omitempty"`
}

type SkillResource struct {
	Path    string `json:"path"`
	Hash    string `json:"hash"`
	Content string `json:"content"`
}

type SkillResourcePage struct {
	Name       string `json:"name"`
	Path       string `json:"path"`
	Hash       string `json:"hash"`
	Content    string `json:"content"`
	Offset     int    `json:"offset"`
	NextOffset int    `json:"next_offset"`
	TotalBytes int    `json:"total_bytes"`
	Truncated  bool   `json:"truncated"`
}
