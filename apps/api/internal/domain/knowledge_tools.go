package domain

const (
	KnowledgeSearchToolName = "knowledge_search"
	KnowledgeReadToolName   = "knowledge_read"
)

// KnowledgeMatch is a locator issued by a successful scoped search, not citation evidence.
type KnowledgeMatch struct {
	Reference       string                `json:"reference"`
	DocumentID      string                `json:"document_id"`
	DocumentTitle   string                `json:"document_title"`
	DocumentVersion string                `json:"document_version"`
	ContentHash     string                `json:"content_hash"`
	IndexIdentity   DocumentIndexIdentity `json:"index_identity"`
	Preview         string                `json:"preview"`
}

type KnowledgeToolSearchResult struct {
	Query         string           `json:"query"`
	Items         []KnowledgeMatch `json:"items"`
	NoMatch       bool             `json:"no_match"`
	Reason        string           `json:"reason,omitempty"`
	GateVersion   string           `json:"gate_version"`
	TrustBoundary string           `json:"trust_boundary"`
}

type KnowledgeToolReadResult struct {
	Reference     string      `json:"reference"`
	Content       string      `json:"content"`
	Offset        int         `json:"offset"`
	NextOffset    int         `json:"next_offset"`
	TotalBytes    int         `json:"total_bytes"`
	Truncated     bool        `json:"truncated"`
	Source        RAGCitation `json:"source"`
	TrustBoundary string      `json:"trust_boundary"`
}
