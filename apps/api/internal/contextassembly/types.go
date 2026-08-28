package contextassembly

import (
	"context"
	"encoding/json"
	"fmt"

	"agentflow-platform/apps/api/internal/domain"
	eventpkg "agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/failure"
)

const (
	AssemblerVersion      = "context-assembler-v1"
	CompactionModeAuto    = "auto"
	CompactionModeOff     = "off"
	CompactionTriggerSoft = "soft"
	CompactionTriggerHard = "hard"
	// CompactionTriggerOverflow is a forced, single-attempt recovery after the
	// provider rejects an otherwise assembled request for context length.
	CompactionTriggerOverflow = "provider_overflow"
)

const (
	SourceSystem         = "system"
	SourceToolDefinition = "tool_definition"
	SourceHistory        = "history"
	SourceCurrentInput   = "current_input"
	SourceMemory         = "memory"
	SourceKnowledge      = "knowledge"
	SourceCompaction     = "compaction_summary"
	SourceHistorySearch  = "session_history_retrieval"
	SourceToolCall       = "tool_call"
	SourceToolResult     = "tool_result"
	SourceTaskState      = "task_state"
)

var ErrInputBudgetExceeded = failure.New(failure.Definition{
	Message: "context input budget exceeded",
	Info: failure.Info{
		Code: "context_input_budget_exceeded", Source: "context_assembly",
		Category: failure.CategoryCapacity, Retryable: false,
	},
})

type InputBudgetError struct {
	RequiredTokens  int
	AvailableTokens int
}

func (e *InputBudgetError) Error() string {
	return fmt.Sprintf("%v: required=%d available=%d", ErrInputBudgetExceeded, e.RequiredTokens, e.AvailableTokens)
}

func (e *InputBudgetError) Unwrap() error { return ErrInputBudgetExceeded }

func (e *InputBudgetError) FailureInfo() failure.Info {
	if e == nil {
		return failure.Info{Code: "context_input_budget_exceeded", Source: "context_assembly", Category: failure.CategoryCapacity}
	}
	return failure.Info{
		Code: "context_input_budget_exceeded", Source: "context_assembly", Category: failure.CategoryCapacity,
		Details: map[string]any{"required_tokens": e.RequiredTokens, "available_tokens": e.AvailableTokens},
	}
}

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
	Config        domain.ContextAssemblyConfig
	Sink          eventpkg.Sink
	History       []domain.Message
	CurrentInput  string
	Memories      []domain.RetrievedMemory
	Knowledge     []domain.RetrievedDocumentChunk
	HistorySearch []domain.RetrievedSessionHistory
	Compaction    *domain.ContextCompaction
	LoadTaskState func() (domain.TaskState, bool, error)
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
		KnowledgeMaxTokens: 16000, ToolResultMaxTokens: 2000, CompactionMode: CompactionModeAuto,
		HistoryRetrievalEnabled: true, HistoryRetrievalMaxResults: 8,
		HistoryRetrievalMaxChars: 12000, HistoryRetrievalMaxTokens: 3000, HistoryRetrievalWindow: 1,
		CompactionSoftThreshold: 0.70,
		CompactionHardThreshold: 0.85, CompactionRecentTokens: 16000,
		CompactionSummaryMaxTokens: 2000, CompactionTimeoutMS: 45000,
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
	if config.ToolResultMaxTokens <= 0 {
		config.ToolResultMaxTokens = defaults.ToolResultMaxTokens
	}
	if config.HistoryRetrievalMaxResults <= 0 {
		config.HistoryRetrievalMaxResults = defaults.HistoryRetrievalMaxResults
	}
	if config.HistoryRetrievalMaxChars <= 0 {
		config.HistoryRetrievalMaxChars = defaults.HistoryRetrievalMaxChars
	}
	if config.HistoryRetrievalMaxTokens <= 0 {
		config.HistoryRetrievalMaxTokens = defaults.HistoryRetrievalMaxTokens
	}
	if config.HistoryRetrievalWindow < 0 {
		config.HistoryRetrievalWindow = defaults.HistoryRetrievalWindow
	}
	switch config.CompactionMode {
	case CompactionModeOff:
	default:
		config.CompactionMode = CompactionModeAuto
	}
	if config.CompactionSoftThreshold <= 0 || config.CompactionSoftThreshold >= 1 {
		config.CompactionSoftThreshold = defaults.CompactionSoftThreshold
	}
	if config.CompactionHardThreshold <= config.CompactionSoftThreshold || config.CompactionHardThreshold >= 1 {
		config.CompactionHardThreshold = defaults.CompactionHardThreshold
	}
	if config.CompactionRecentTokens <= 0 {
		config.CompactionRecentTokens = defaults.CompactionRecentTokens
	}
	if config.CompactionRecentTokens >= config.HistoryMaxTokens {
		config.CompactionRecentTokens = max(1, config.HistoryMaxTokens/4)
	}
	if config.CompactionSummaryMaxTokens <= 0 {
		config.CompactionSummaryMaxTokens = defaults.CompactionSummaryMaxTokens
	}
	if config.CompactionTimeoutMS <= 0 {
		config.CompactionTimeoutMS = defaults.CompactionTimeoutMS
	}
	return config
}

func NormalizeSnapshotConfig(config domain.ContextAssemblyConfig, schemaVersion int) domain.ContextAssemblyConfig {
	config = NormalizeConfig(config)
	if schemaVersion < domain.CompactionRuntimeSnapshotVersion {
		config.CompactionMode = CompactionModeOff
	}
	if schemaVersion < domain.SessionHistorySnapshotVersion {
		config.HistoryRetrievalEnabled = false
	} else {
		config.HistoryRetrievalEnabled = true
	}
	return config
}
