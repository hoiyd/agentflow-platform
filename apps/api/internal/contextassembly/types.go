package contextassembly

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"agentflow-platform/apps/api/internal/domain"
	eventpkg "agentflow-platform/apps/api/internal/event"
)

const AssemblerVersion = "context-assembler-v1"

const (
	SourceSystem         = "system"
	SourceToolDefinition = "tool_definition"
	SourceHistory        = "history"
	SourceCurrentInput   = "current_input"
	SourceMemory         = "memory"
	SourceKnowledge      = "knowledge"
	SourceToolCall       = "tool_call"
	SourceToolResult     = "tool_result"
)

var ErrInputBudgetExceeded = errors.New("context input budget exceeded")

type InputBudgetError struct {
	RequiredTokens  int
	AvailableTokens int
}

func (e *InputBudgetError) Error() string {
	return fmt.Sprintf("%v: required=%d available=%d", ErrInputBudgetExceeded, e.RequiredTokens, e.AvailableTokens)
}

func (e *InputBudgetError) Unwrap() error { return ErrInputBudgetExceeded }

type Message struct {
	Source      string
	ReferenceID string
	Role        string
	Content     string
	ToolCallID  string
	ToolCalls   json.RawMessage
}

type Tool struct {
	Name       string
	Definition map[string]any
}

type Request struct {
	Model    string
	Messages []Message
	Tools    []Tool
}

type Pack struct {
	Messages []Message
	Manifest domain.ContextManifest
}

type Session struct {
	Config       domain.ContextAssemblyConfig
	Sink         eventpkg.Sink
	History      []domain.Message
	CurrentInput string
	Memories     []domain.RetrievedMemory
	Knowledge    []domain.RetrievedDocumentChunk
}

type sessionKey struct{}

func WithSession(ctx context.Context, session Session) context.Context {
	return context.WithValue(ctx, sessionKey{}, session)
}

func sessionFromContext(ctx context.Context) (Session, bool) {
	if ctx == nil {
		return Session{}, false
	}
	session, ok := ctx.Value(sessionKey{}).(Session)
	return session, ok
}

func DefaultConfig() domain.ContextAssemblyConfig {
	return domain.ContextAssemblyConfig{
		AssemblerVersion: AssemblerVersion, ContextWindowTokens: 128000, OutputReserveTokens: 8192,
		SafetyMarginTokens: 4096, HistoryMaxTokens: 64000, MemoryMaxTokens: 8000,
		KnowledgeMaxTokens: 16000,
	}
}

func NormalizeConfig(config domain.ContextAssemblyConfig) domain.ContextAssemblyConfig {
	defaults := DefaultConfig()
	if config.AssemblerVersion == "" {
		config.AssemblerVersion = defaults.AssemblerVersion
	}
	if config.ContextWindowTokens <= 0 {
		config.ContextWindowTokens = defaults.ContextWindowTokens
	}
	if config.OutputReserveTokens <= 0 {
		config.OutputReserveTokens = defaults.OutputReserveTokens
	}
	if config.SafetyMarginTokens <= 0 {
		config.SafetyMarginTokens = defaults.SafetyMarginTokens
	}
	if config.OutputReserveTokens+config.SafetyMarginTokens >= config.ContextWindowTokens {
		config.OutputReserveTokens = max(1, config.ContextWindowTokens/8)
		config.SafetyMarginTokens = max(1, config.ContextWindowTokens/16)
	}
	if config.HistoryMaxTokens <= 0 {
		config.HistoryMaxTokens = defaults.HistoryMaxTokens
	}
	if config.MemoryMaxTokens <= 0 {
		config.MemoryMaxTokens = defaults.MemoryMaxTokens
	}
	if config.KnowledgeMaxTokens <= 0 {
		config.KnowledgeMaxTokens = defaults.KnowledgeMaxTokens
	}
	return config
}
