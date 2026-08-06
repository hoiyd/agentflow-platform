package domain

import "time"

type Document struct {
	ID             string         `json:"id"`
	WorkspaceID    string         `json:"workspace_id,omitempty"`
	Title          string         `json:"title"`
	Version        string         `json:"version,omitempty"`
	ContentHash    string         `json:"content_hash,omitempty"`
	SourceType     string         `json:"source_type"`
	SourceURI      string         `json:"source_uri,omitempty"`
	MimeType       string         `json:"mime_type,omitempty"`
	Content        string         `json:"-"`
	Metadata       map[string]any `json:"metadata"`
	ChunkCount     int            `json:"chunk_count,omitempty"`
	EmbeddingCount int            `json:"embedding_count,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
}

type ChunkSource struct {
	ParentID        string   `json:"parent_id,omitempty"`
	SectionPath     []string `json:"section_path"`
	StartOffset     int      `json:"start_offset"`
	EndOffset       int      `json:"end_offset"`
	DocumentVersion string   `json:"document_version,omitempty"`
	ContentHash     string   `json:"content_hash,omitempty"`
}

type DocumentChunk struct {
	ID         string `json:"id"`
	DocumentID string `json:"document_id"`
	ChunkSource
	ChunkIndex int            `json:"chunk_index"`
	Content    string         `json:"content"`
	TokenCount int            `json:"token_count"`
	Metadata   map[string]any `json:"metadata"`
	CreatedAt  time.Time      `json:"created_at"`
	Document   Document       `json:"document,omitempty"`
}

type DocumentChunkEmbedding struct {
	ChunkID    string    `json:"chunk_id"`
	Provider   string    `json:"provider"`
	Model      string    `json:"model"`
	Dimensions int       `json:"dimensions"`
	Embedding  []float64 `json:"-"`
	CreatedAt  time.Time `json:"created_at"`
}

type DocumentIngestRequest struct {
	WorkspaceID string         `json:"workspace_id,omitempty"`
	Title       string         `json:"title"`
	Version     string         `json:"version,omitempty"`
	Content     string         `json:"content"`
	SourceType  string         `json:"source_type,omitempty"`
	SourceURI   string         `json:"source_uri,omitempty"`
	MimeType    string         `json:"mime_type,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

type DocumentSearch struct {
	Query                     string            `json:"query"`
	Embedding                 []float64         `json:"-"`
	EmbeddingProvider         string            `json:"-"`
	EmbeddingModel            string            `json:"-"`
	LexicalTerms              []string          `json:"-"`
	WorkspaceID               string            `json:"workspace_id,omitempty"`
	Metadata                  map[string]string `json:"metadata,omitempty"`
	Limit                     int               `json:"limit,omitempty"`
	MinSimilarity             float64           `json:"min_similarity,omitempty"`
	KnowledgeContextMaxTokens int               `json:"knowledge_context_max_tokens,omitempty"`
}

type DocumentContextSearch struct {
	DocumentID     string
	WorkspaceID    string
	ParentID       string
	ChunkIndex     int
	NeighborWindow int
	Metadata       map[string]string
}

const (
	ContextRoleMatchedChild = "matched_child"
	ContextRoleParent       = "parent"
	ContextRoleAdjacent     = "adjacent"
)

type RetrievedDocumentChunk struct {
	Document         Document      `json:"document"`
	Chunk            DocumentChunk `json:"chunk"`
	Similarity       float64       `json:"similarity"`
	RecencyBoost     float64       `json:"recency_boost"`
	Score            float64       `json:"score"`
	VectorRank       int           `json:"vector_rank,omitempty"`
	LexicalRank      int           `json:"lexical_rank,omitempty"`
	LexicalScore     float64       `json:"lexical_score,omitempty"`
	RRFScore         float64       `json:"rrf_score,omitempty"`
	FusionRank       int           `json:"fusion_rank,omitempty"`
	RerankRank       int           `json:"rerank_rank,omitempty"`
	LexicalBoost     float64       `json:"lexical_boost,omitempty"`
	MetadataBoost    float64       `json:"metadata_boost,omitempty"`
	DiversityPenalty float64       `json:"diversity_penalty,omitempty"`
	RerankScore      float64       `json:"rerank_score,omitempty"`
	MatchedTerms     []string      `json:"matched_terms,omitempty"`
	EvidenceScore    float64       `json:"evidence_score,omitempty"`
	EvidenceCoverage float64       `json:"evidence_coverage,omitempty"`
	Confidence       string        `json:"confidence,omitempty"`
	FilterReason     string        `json:"filter_reason,omitempty"`
	ContextRole      string        `json:"context_role,omitempty"`
	MatchedChunkID   string        `json:"matched_chunk_id,omitempty"`
	SourceChunkIDs   []string      `json:"source_chunk_ids,omitempty"`
	MatchedChunkIDs  []string      `json:"matched_chunk_ids,omitempty"`
	MergedChunkCount int           `json:"merged_chunk_count,omitempty"`
	SourceID         string        `json:"source_id,omitempty"`
}

// RAGCitation is trusted source metadata resolved by the server. The model may
// emit only the human-readable SourceID marker; it cannot supply these fields.
type RAGCitation struct {
	SourceID        string   `json:"source_id"`
	DocumentID      string   `json:"document_id"`
	DocumentTitle   string   `json:"document_title"`
	DocumentVersion string   `json:"document_version,omitempty"`
	ChunkID         string   `json:"chunk_id"`
	SourceChunkIDs  []string `json:"source_chunk_ids,omitempty"`
	SectionPath     []string `json:"section_path,omitempty"`
	StartOffset     int      `json:"start_offset"`
	EndOffset       int      `json:"end_offset"`
}

type EmbeddingInfo struct {
	Provider   string `json:"provider"`
	Model      string `json:"model"`
	Dimensions int    `json:"dimensions"`
	Estimated  bool   `json:"estimated"`
}

type FusionInfo struct {
	Algorithm     string  `json:"algorithm"`
	Version       string  `json:"version"`
	RankConstant  int     `json:"rank_constant"`
	DenseWeight   float64 `json:"dense_weight"`
	LexicalWeight float64 `json:"lexical_weight"`
}

// RerankerInfo identifies the active ranking implementation and its immutable
// configuration. Provider and Model are reserved for future model-backed
// implementations such as a Cross-Encoder.
type RerankerInfo struct {
	Algorithm     string `json:"algorithm"`
	Version       string `json:"version"`
	ConfigVersion string `json:"config_version"`
	Provider      string `json:"provider,omitempty"`
	Model         string `json:"model,omitempty"`
}

type RelevanceGateInfo struct {
	Policy        string `json:"policy"`
	Version       string `json:"version"`
	ConfigVersion string `json:"config_version"`
}

type KnowledgeSecurityDecision struct {
	DocumentID string   `json:"document_id"`
	ChunkID    string   `json:"chunk_id"`
	Action     string   `json:"action"`
	Reasons    []string `json:"reasons"`
}

type KnowledgeSecurityInfo struct {
	PolicyVersion     string                      `json:"policy_version"`
	UntrustedContext  bool                        `json:"untrusted_context"`
	CheckedCandidates int                         `json:"checked_candidates"`
	BlockedCandidates int                         `json:"blocked_candidates"`
	Decisions         []KnowledgeSecurityDecision `json:"decisions,omitempty"`
}

type ContextSelectionInfo struct {
	Version         string                     `json:"version"`
	MaxTokens       int                        `json:"max_tokens"`
	TokensUsed      int                        `json:"tokens_used"`
	MatchedChildren int                        `json:"matched_children"`
	ParentChunks    int                        `json:"parent_chunks"`
	AdjacentChunks  int                        `json:"adjacent_chunks"`
	ScopeFiltered   bool                       `json:"scope_filtered"`
	Transformation  *ContextTransformationInfo `json:"transformation,omitempty"`
}

// ContextTransformationInfo describes versioned post-selection context shaping.
// The name is intentionally broader than deduplication and merging so future
// transformers can report operations such as compression, truncation, or reordering.
type ContextTransformationInfo struct {
	Version           string `json:"version"`
	InputChunks       int    `json:"input_chunks"`
	OutputChunks      int    `json:"output_chunks"`
	DuplicatesRemoved int    `json:"duplicates_removed"`
	AdjacentMerges    int    `json:"adjacent_merges"`
	DocumentGroups    int    `json:"document_groups"`
}

type DocumentSearchResponse struct {
	Items            []RetrievedDocumentChunk `json:"items"`
	ContextItems     []RetrievedDocumentChunk `json:"context_items,omitempty"`
	CitationSources  []RAGCitation            `json:"citation_sources,omitempty"`
	ContextSelection ContextSelectionInfo     `json:"context_selection"`
	Embedding        EmbeddingInfo            `json:"embedding"`
	Fusion           FusionInfo               `json:"fusion"`
	Reranker         RerankerInfo             `json:"reranker"`
	RelevanceGate    RelevanceGateInfo        `json:"relevance_gate"`
	Security         KnowledgeSecurityInfo    `json:"security"`
	NoMatch          bool                     `json:"no_match,omitempty"`
	Reason           string                   `json:"reason,omitempty"`
}

const RAGGoldenDatasetSchemaVersion = "rag-golden-dataset-v1"

type RAGGoldenSource struct {
	DocumentID      string   `json:"document_id,omitempty"`
	ChunkID         string   `json:"chunk_id,omitempty"`
	SourceURI       string   `json:"source_uri,omitempty"`
	ContentContains []string `json:"content_contains,omitempty"`
}

type RAGGoldenDataset struct {
	SchemaVersion string              `json:"schema_version"`
	ID            string              `json:"id"`
	Version       string              `json:"version"`
	Description   string              `json:"description,omitempty"`
	Tags          []string            `json:"tags,omitempty"`
	Cases         []RAGEvaluationCase `json:"cases"`
}

type RAGGoldenDatasetInfo struct {
	SchemaVersion string   `json:"schema_version"`
	ID            string   `json:"id"`
	Version       string   `json:"version"`
	Description   string   `json:"description,omitempty"`
	Tags          []string `json:"tags,omitempty"`
}

type RAGEvaluationCase struct {
	ID                    string            `json:"id"`
	Query                 string            `json:"query"`
	Answerable            *bool             `json:"answerable,omitempty"`
	ExpectedSources       []RAGGoldenSource `json:"expected_sources,omitempty"`
	ForbiddenSources      []RAGGoldenSource `json:"forbidden_sources,omitempty"`
	RequiredSourceCount   int               `json:"required_source_count,omitempty"`
	ExpectedDocumentIDs   []string          `json:"expected_document_ids,omitempty"`
	ExpectedChunkIDs      []string          `json:"expected_chunk_ids,omitempty"`
	ExpectedChunkContains []string          `json:"expected_chunk_contains,omitempty"`
	MinAcceptableRank     int               `json:"min_acceptable_rank,omitempty"`
	Tags                  []string          `json:"tags,omitempty"`
}

type RAGEvaluationRunRequest struct {
	Dataset       *RAGGoldenDataset   `json:"dataset,omitempty"`
	Cases         []RAGEvaluationCase `json:"cases,omitempty"`
	WorkspaceID   string              `json:"workspace_id,omitempty"`
	Metadata      map[string]string   `json:"metadata,omitempty"`
	TopK          int                 `json:"top_k,omitempty"`
	MinSimilarity float64             `json:"min_similarity,omitempty"`
}

type RAGEvaluationSummary struct {
	Total             int `json:"total"`
	AnswerableCases   int `json:"answerable_cases"`
	UnanswerableCases int `json:"unanswerable_cases"`
	HitAt1            int `json:"hit_at_1"`
	HitAt3            int `json:"hit_at_3"`
	HitAt5            int `json:"hit_at_5"`
	Misses            int `json:"misses"`
	BlockedCandidates int `json:"blocked_candidates,omitempty"`
}

type RAGEvaluationCaseResult struct {
	ID                    string                   `json:"id"`
	Query                 string                   `json:"query"`
	Answerable            bool                     `json:"answerable"`
	ExpectedSources       []RAGGoldenSource        `json:"expected_sources,omitempty"`
	ForbiddenSources      []RAGGoldenSource        `json:"forbidden_sources,omitempty"`
	RequiredSourceCount   int                      `json:"required_source_count,omitempty"`
	ExpectedDocumentIDs   []string                 `json:"expected_document_ids,omitempty"`
	ExpectedChunkIDs      []string                 `json:"expected_chunk_ids,omitempty"`
	ExpectedChunkContains []string                 `json:"expected_chunk_contains,omitempty"`
	Tags                  []string                 `json:"tags,omitempty"`
	Hit                   bool                     `json:"hit"`
	HitAt1                bool                     `json:"hit_at_1"`
	HitAt3                bool                     `json:"hit_at_3"`
	HitAt5                bool                     `json:"hit_at_5"`
	BestRank              int                      `json:"best_rank,omitempty"`
	FailureReason         string                   `json:"failure_reason,omitempty"`
	Items                 []RetrievedDocumentChunk `json:"items"`
	Security              KnowledgeSecurityInfo    `json:"security"`
}

type RAGEvaluationRunResponse struct {
	Dataset       *RAGGoldenDatasetInfo     `json:"dataset,omitempty"`
	Summary       RAGEvaluationSummary      `json:"summary"`
	Cases         []RAGEvaluationCaseResult `json:"cases"`
	Embedding     EmbeddingInfo             `json:"embedding"`
	Fusion        FusionInfo                `json:"fusion"`
	Reranker      RerankerInfo              `json:"reranker"`
	RelevanceGate RelevanceGateInfo         `json:"relevance_gate"`
}
