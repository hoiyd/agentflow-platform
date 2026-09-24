package provider

import (
	"context"

	"agentflow-platform/apps/api/internal/domain"
	eventpkg "agentflow-platform/apps/api/internal/event"
)

// RuntimeIdentity is the provider-neutral model configuration frozen into a Run.
type RuntimeIdentity struct {
	Provider            string
	BaseURL             string
	Model               string
	EmbeddingBaseURL    string
	EmbeddingModel      string
	EmbeddingDimensions int
	EmbeddingProvider   string
	GenerationPolicy    domain.GenerationPolicy
	SeedSupported       bool
}

type Message struct {
	Role        string     `json:"role"`
	Content     string     `json:"content,omitempty"`
	ToolCallID  string     `json:"tool_call_id,omitempty"`
	ToolCalls   []ToolCall `json:"tool_calls,omitempty"`
	Refusal     string     `json:"refusal,omitempty"`
	Source      string     `json:"-"`
	ReferenceID string     `json:"-"`
}

type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type StreamEvent struct {
	Type       string
	Delta      string
	ToolName   string
	ToolCallID string
	Error      string
}

// ChatRequest is the provider-neutral input for one agent turn.
type ChatRequest struct {
	SystemPrompt string
	History      []domain.Message
	Latest       string
	ToolNames    []string
	Definitions  []map[string]any
}

type PreparedChat struct {
	RawMessages  []Message
	Messages     []Message
	Manifest     domain.ContextManifest
	Latest       string
	SystemPrompt string
}

type ChatChoice struct {
	Content   string
	ToolCalls []ToolCall
}

type ChatTrace struct {
	Recorder  *eventpkg.Recorder
	RunID     string
	StepID    string
	Memories  []domain.RetrievedMemory
	Knowledge []domain.RetrievedDocumentChunk
}

type ChatStreamKind string

const (
	ChatStreamAnswer     ChatStreamKind = "answer_stream"
	ChatStreamToolResult ChatStreamKind = "tool_result_response"
)

// ChatModel exposes model calls; the caller owns Tool execution and follow-up sequencing.
type ChatModel interface {
	HasAPIKey() bool
	PrepareAgentChat(context.Context, ChatRequest) (PreparedChat, error)
	PrepareFollowup(context.Context, []Message) (PreparedChat, error)
	SelectTools(context.Context, PreparedChat, []map[string]any, ChatTrace) (ChatChoice, error)
	StreamAnswer(context.Context, PreparedChat, ChatStreamKind, ChatTrace, chan<- StreamEvent) (bool, error)
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
	Estimated        bool
}

func (u Usage) Valid() bool {
	return u.PromptTokens > 0 || u.CompletionTokens > 0 || u.TotalTokens > 0
}

type TextCompletion struct {
	Text  string
	Model string
	Usage Usage
}

type PreparedText struct {
	Messages []Message
	Manifest domain.ContextManifest
}

type Embedding struct {
	Vector     []float64
	Model      string
	Provider   string
	Estimated  bool
	Dimensions int
}

// Client is the provider-neutral capability contract used by orchestration.
// Provider adapters may implement OpenAI-compatible, local, or future native APIs.
type Client interface {
	ChatModel
	RuntimeIdentity() RuntimeIdentity
	WithRuntimeIdentity(RuntimeIdentity) Client
	CompleteTextDetailed(context.Context, string, string) (TextCompletion, error)
	PrepareText(context.Context, string, string) (PreparedText, error)
	CompletePreparedText(context.Context, PreparedText) (TextCompletion, error)
	EmbedText(context.Context, string) (Embedding, error)
}

type TextCompleter interface {
	CompleteTextDetailed(context.Context, string, string) (TextCompletion, error)
}

type Embedder interface {
	EmbedText(context.Context, string) (Embedding, error)
}
