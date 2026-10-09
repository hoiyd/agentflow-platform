package tool

import (
	"context"
	"encoding/json"
	"time"

	"agentflow-platform/apps/api/internal/tool/policy"
)

type Descriptor struct {
	Name               string            `json:"name"`
	Description        string            `json:"description"`
	Parameters         map[string]any    `json:"parameters"`
	SchemaVersion      string            `json:"schema_version"`
	DefinitionRevision string            `json:"definition_revision"`
	Concurrency        ConcurrencyPolicy `json:"concurrency,omitempty"`
	SideEffect         SideEffectPolicy  `json:"side_effect,omitempty"`
	Security           policy.Capability `json:"security"`
}

type SideEffectMode string

const (
	SideEffectNone     SideEffectMode = "none"
	SideEffectInternal SideEffectMode = "internal"
	SideEffectExternal SideEffectMode = "external"
)

// SideEffectPolicy declares recovery capabilities, not a second write class.
// Security.SideEffect is authoritative; JournalMode derives the durable boundary.
type SideEffectPolicy struct {
	RetryWithSameKey bool `json:"retry_with_same_key,omitempty"`
	Compensate       bool `json:"compensate,omitempty"`
}

// JournalMode is derived from validated Security, never separately declared by
// a Binding. Empty means no journal and preserves existing read-only snapshots.
// Internal writes accept a real Turn or Stage; external writes require a Stage.
func (d Descriptor) JournalMode() SideEffectMode {
	switch d.Security.SideEffect {
	case policy.SideEffectInternalWrite:
		return SideEffectInternal
	case policy.SideEffectExternalWrite, policy.SideEffectDestructive:
		return SideEffectExternal
	default:
		return ""
	}
}

func (d Descriptor) RequiresJournal() bool {
	return d.JournalMode() != ""
}

type ConcurrencyMode string

const (
	ConcurrencySerial   ConcurrencyMode = "serial"
	ConcurrencyReadOnly ConcurrencyMode = "read_only"
	ConcurrencyKeyed    ConcurrencyMode = "keyed"
)

type ConcurrencyPolicy struct {
	Mode        ConcurrencyMode `json:"mode,omitempty"`
	KeyArgument string          `json:"key_argument,omitempty"`
}

type Handler func(ctx context.Context, args json.RawMessage) (any, error)

// ScopeResolver derives the concrete scope requested by one call from trusted
// Binding code. It may narrow, but never widen, Descriptor.Security.Scope.
type ScopeResolver func(ctx context.Context, args json.RawMessage) (policy.Scope, error)

type ExecutionPolicy struct {
	Timeout        time.Duration
	MaxResultBytes int
}

type EffectReconciliationContext struct {
	CommandID       string
	IdempotencyKey  string
	CompensationKey string
	RunID           string
	StageID         string
	TurnID          string
	ToolCallID      string
	ToolName        string
	RequestHash     string
}

// SideEffectReconciliation contains Tool-specific, idempotent recovery hooks.
// Retry must reuse IdempotencyKey; Compensate must use CompensationKey.
type SideEffectReconciliation struct {
	RetryWithSameKey func(context.Context, EffectReconciliationContext) (any, error)
	Compensate       func(context.Context, EffectReconciliationContext) error
}

type Binding struct {
	Descriptor        Descriptor
	Handler           Handler
	Policy            ExecutionPolicy
	ResolveScope      ScopeResolver
	Reconciliation    SideEffectReconciliation
	UnavailableReason string
	contract          *argumentContract
}

type ToolInfo struct {
	ServiceEnabled    bool           `json:"service_enabled"`
	WorkspaceEnabled  bool           `json:"workspace_enabled"`
	ConfigRevision    int64          `json:"config_revision"`
	ExcludedReason    string         `json:"excluded_reason,omitempty"`
	Name              string         `json:"name"`
	Description       string         `json:"description"`
	Parameters        map[string]any `json:"parameters"`
	Enabled           bool           `json:"enabled"`
	UnavailableReason string         `json:"unavailable_reason,omitempty"`
}

func ObjectSchema(properties map[string]any, required []string) map[string]any {
	if properties == nil {
		properties = map[string]any{}
	}
	if required == nil {
		required = []string{}
	}
	return map[string]any{
		"type":                 "object",
		"properties":           properties,
		"required":             required,
		"additionalProperties": false,
	}
}
