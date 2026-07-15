package contextassembly

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"agentflow-platform/apps/api/internal/domain"
	eventpkg "agentflow-platform/apps/api/internal/event"
)

const PolicyVersion = "context-v1"

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
	Policy       domain.RuntimeContextPolicy
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

func DefaultPolicy() domain.RuntimeContextPolicy {
	return domain.RuntimeContextPolicy{
		Version: PolicyVersion, ContextWindowTokens: 128000, OutputReserveTokens: 8192,
		SafetyMarginTokens: 4096, HistoryMaxTokens: 64000, MemoryMaxTokens: 8000,
		KnowledgeMaxTokens: 16000,
	}
}

func NormalizePolicy(policy domain.RuntimeContextPolicy) domain.RuntimeContextPolicy {
	defaults := DefaultPolicy()
	if policy.Version == "" {
		policy.Version = defaults.Version
	}
	if policy.ContextWindowTokens <= 0 {
		policy.ContextWindowTokens = defaults.ContextWindowTokens
	}
	if policy.OutputReserveTokens <= 0 {
		policy.OutputReserveTokens = defaults.OutputReserveTokens
	}
	if policy.SafetyMarginTokens <= 0 {
		policy.SafetyMarginTokens = defaults.SafetyMarginTokens
	}
	if policy.OutputReserveTokens+policy.SafetyMarginTokens >= policy.ContextWindowTokens {
		policy.OutputReserveTokens = max(1, policy.ContextWindowTokens/8)
		policy.SafetyMarginTokens = max(1, policy.ContextWindowTokens/16)
	}
	if policy.HistoryMaxTokens <= 0 {
		policy.HistoryMaxTokens = defaults.HistoryMaxTokens
	}
	if policy.MemoryMaxTokens <= 0 {
		policy.MemoryMaxTokens = defaults.MemoryMaxTokens
	}
	if policy.KnowledgeMaxTokens <= 0 {
		policy.KnowledgeMaxTokens = defaults.KnowledgeMaxTokens
	}
	return policy
}
