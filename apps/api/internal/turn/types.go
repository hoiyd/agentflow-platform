package turn

import (
	"context"
	"errors"
	"strings"

	"agentflow-platform/apps/api/internal/domain"
	eventpkg "agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/tools"
)

type StopReason string
type ModelMode string

const (
	StopCompleted StopReason = "completed"
	StopCanceled  StopReason = "canceled"
	StopFailed    StopReason = "failed"
)

const (
	ModelModeAgentStream ModelMode = "agent_stream"
	ModelModeText        ModelMode = "text"
)

var ErrInvalidRequest = errors.New("invalid turn request")

type Request struct {
	RunID          string
	StepID         string
	TurnID         string
	ConversationID string
	Agent          domain.Agent
	Role           string
	SystemPrompt   string
	History        []domain.Message
	Input          string
	ExecutorKind   string
	ModelMode      ModelMode
	Catalog        *tools.Catalog
	Context        Context
	Metadata       map[string]any
	Sink           eventpkg.Sink
}

type Context struct {
	Memories []domain.RetrievedMemory
	Chunks   []domain.RetrievedDocumentChunk
}

type Usage struct {
	Model            string
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	Estimated        bool
}

type Result struct {
	Output     string
	Usage      Usage
	StopReason StopReason
}

type ModelEvent struct {
	Type       EventType
	Delta      string
	ToolName   string
	ToolCallID string
	Error      string
}

type Model interface {
	Execute(context.Context, Request, func(ModelEvent)) (Result, error)
}

func (r Request) Validate() error {
	if strings.TrimSpace(r.Input) == "" {
		return errors.Join(ErrInvalidRequest, errors.New("turn input is required"))
	}
	return nil
}
