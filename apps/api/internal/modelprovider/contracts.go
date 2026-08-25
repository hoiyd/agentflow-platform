package modelprovider

import (
	"context"

	"agentflow-platform/apps/api/internal/domain"
	eventpkg "agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/tools"
)

// RuntimeIdentity is the provider-neutral model configuration frozen into a Run.
type RuntimeIdentity struct {
	Provider            string
	BaseURL             string
	Model               string
	EmbeddingBaseURL    string
	EmbeddingModel      string
	EmbeddingDimensions int
}

type Message struct {
	Role        string     `json:"role"`
	Content     string     `json:"content,omitempty"`
	ToolCallID  string     `json:"tool_call_id,omitempty"`
	ToolCalls   []ToolCall `json:"tool_calls,omitempty"`
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
	HasAPIKey() bool
	RuntimeIdentity() RuntimeIdentity
	WithRuntimeIdentity(RuntimeIdentity) Client
	StreamAgentChatWithToolsTrace(context.Context, string, []domain.Message, string, *tools.Catalog, *eventpkg.Recorder, string, string, []domain.RetrievedMemory, []domain.RetrievedDocumentChunk) (<-chan StreamEvent, <-chan error)
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
