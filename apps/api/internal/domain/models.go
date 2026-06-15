package domain

import "time"

type Conversation struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Message struct {
	ID             string    `json:"id"`
	ConversationID string    `json:"conversation_id"`
	Role           string    `json:"role"`
	Content        string    `json:"content"`
	CreatedAt      time.Time `json:"created_at"`
}

type Agent struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Description  string    `json:"description"`
	SystemPrompt string    `json:"system_prompt"`
	Tools        []string  `json:"tools"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type RunStatus string

const (
	RunQueued         RunStatus = "queued"
	RunRunning        RunStatus = "running"
	RunWaitingForUser RunStatus = "waiting_for_user"
	RunCompleted      RunStatus = "completed"
	RunFailed         RunStatus = "failed"
	RunCanceling      RunStatus = "canceling"
	RunCanceled       RunStatus = "canceled"
)

type Run struct {
	ID             string     `json:"id"`
	AgentID        string     `json:"agent_id"`
	ConversationID string     `json:"conversation_id"`
	Status         RunStatus  `json:"status"`
	Error          string     `json:"error,omitempty"`
	StartedAt      *time.Time `json:"started_at,omitempty"`
	CompletedAt    *time.Time `json:"completed_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

type CollaborationStepStatus string

const (
	CollaborationStepQueued    CollaborationStepStatus = "queued"
	CollaborationStepRunning   CollaborationStepStatus = "running"
	CollaborationStepCompleted CollaborationStepStatus = "completed"
	CollaborationStepFailed    CollaborationStepStatus = "failed"
)

type CollaborationStep struct {
	ID             string                  `json:"id"`
	RunID          string                  `json:"run_id"`
	ConversationID string                  `json:"conversation_id"`
	Role           string                  `json:"role"`
	AgentID        string                  `json:"agent_id,omitempty"`
	Status         CollaborationStepStatus `json:"status"`
	Iteration      int                     `json:"iteration,omitempty"`
	Input          string                  `json:"input"`
	Output         string                  `json:"output"`
	Error          string                  `json:"error,omitempty"`
	CreatedAt      time.Time               `json:"created_at"`
	UpdatedAt      time.Time               `json:"updated_at"`
}

type ChatRequest struct {
	ConversationID string `json:"conversation_id"`
	AgentID        string `json:"agent_id"`
	Message        string `json:"message"`
	Mode           string `json:"mode"`
	Executor       string `json:"executor"`
}

type ContinueRunRequest struct {
	Plan string `json:"plan"`
}

type ResumeRunRequest struct {
	UserInput string `json:"user_input"`
}

type ChatChunk struct {
	Type           string `json:"type"`
	ConversationID string `json:"conversation_id,omitempty"`
	RunID          string `json:"run_id,omitempty"`
	AgentID        string `json:"agent_id,omitempty"`
	Status         string `json:"status,omitempty"`
	MessageID      string `json:"message_id,omitempty"`
	Delta          string `json:"delta,omitempty"`
	Error          string `json:"error,omitempty"`
	Role           string `json:"role,omitempty"`
	Iteration      int    `json:"iteration,omitempty"`
	MaxIterations  int    `json:"max_iterations,omitempty"`
	ElapsedSeconds int    `json:"elapsed_seconds,omitempty"`
	MaxRuntimeSec  int    `json:"max_runtime_seconds,omitempty"`
	OutputChars    int    `json:"output_chars,omitempty"`
	MaxOutputChars int    `json:"max_output_chars,omitempty"`
	ToolCalls      int    `json:"tool_calls,omitempty"`
	MaxToolCalls   int    `json:"max_tool_calls,omitempty"`
	StopReason     string `json:"stop_reason,omitempty"`
	Question       string `json:"question,omitempty"`
	Context        string `json:"context,omitempty"`
	Input          string `json:"input,omitempty"`
	Output         string `json:"output,omitempty"`
}

type TraceEventType string

const (
	TraceLLMStart  TraceEventType = "llm_start"
	TraceLLMEnd    TraceEventType = "llm_end"
	TraceToolStart TraceEventType = "tool_start"
	TraceToolEnd   TraceEventType = "tool_end"
	TraceRetrieval TraceEventType = "retrieval"
	TraceError     TraceEventType = "error"
)

type TraceEvent struct {
	ID         string         `json:"id"`
	RunID      string         `json:"run_id"`
	StepID     string         `json:"step_id,omitempty"`
	Type       TraceEventType `json:"type"`
	Payload    map[string]any `json:"payload"`
	Timestamp  time.Time      `json:"timestamp"`
	DurationMS int64          `json:"duration_ms,omitempty"`
}

type RunTraceSummary struct {
	RunID               string    `json:"run_id"`
	Status              RunStatus `json:"status"`
	TotalDurationMS     int64     `json:"total_duration_ms"`
	TotalTokens         int       `json:"total_tokens"`
	PromptTokens        int       `json:"prompt_tokens"`
	CompletionTokens    int       `json:"completion_tokens"`
	TokenUsageEstimated bool      `json:"token_usage_estimated"`
	LLMCalls            int       `json:"llm_calls"`
	ToolCalls           int       `json:"tool_calls"`
	ErrorCount          int       `json:"error_count"`
}

type RunReplay struct {
	Run          Run                 `json:"run"`
	Conversation Conversation        `json:"conversation"`
	Messages     []Message           `json:"messages"`
	Steps        []CollaborationStep `json:"steps"`
	Summary      RunTraceSummary     `json:"summary"`
	Events       []TraceEvent        `json:"events"`
}

type Memory struct {
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

type MemoryEmbedding struct {
	MemoryID   string    `json:"memory_id"`
	Provider   string    `json:"provider"`
	Model      string    `json:"model"`
	Dimensions int       `json:"dimensions"`
	Embedding  []float64 `json:"-"`
	CreatedAt  time.Time `json:"created_at"`
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

type Document struct {
	ID             string         `json:"id"`
	WorkspaceID    string         `json:"workspace_id,omitempty"`
	Title          string         `json:"title"`
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

type DocumentChunk struct {
	ID         string         `json:"id"`
	DocumentID string         `json:"document_id"`
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
	Content     string         `json:"content"`
	SourceType  string         `json:"source_type,omitempty"`
	SourceURI   string         `json:"source_uri,omitempty"`
	MimeType    string         `json:"mime_type,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

type DocumentSearch struct {
	Query             string            `json:"query"`
	Embedding         []float64         `json:"-"`
	EmbeddingProvider string            `json:"-"`
	EmbeddingModel    string            `json:"-"`
	WorkspaceID       string            `json:"workspace_id,omitempty"`
	Metadata          map[string]string `json:"metadata,omitempty"`
	Limit             int               `json:"limit,omitempty"`
	MinSimilarity     float64           `json:"min_similarity,omitempty"`
}

type RetrievedDocumentChunk struct {
	Document         Document      `json:"document"`
	Chunk            DocumentChunk `json:"chunk"`
	Similarity       float64       `json:"similarity"`
	RecencyBoost     float64       `json:"recency_boost"`
	Score            float64       `json:"score"`
	LexicalBoost     float64       `json:"lexical_boost,omitempty"`
	MetadataBoost    float64       `json:"metadata_boost,omitempty"`
	DiversityPenalty float64       `json:"diversity_penalty,omitempty"`
	RerankScore      float64       `json:"rerank_score,omitempty"`
}

type EmbeddingInfo struct {
	Provider   string `json:"provider"`
	Model      string `json:"model"`
	Dimensions int    `json:"dimensions"`
	Estimated  bool   `json:"estimated"`
}

type DocumentSearchResponse struct {
	Items     []RetrievedDocumentChunk `json:"items"`
	Embedding EmbeddingInfo            `json:"embedding"`
}
