package tools

import (
	"context"
	"encoding/json"
	"time"

	"agentflow-platform/apps/api/internal/toolpolicy"
)

type Descriptor struct {
	Name               string                `json:"name"`
	Description        string                `json:"description"`
	Parameters         map[string]any        `json:"parameters"`
	SchemaVersion      string                `json:"schema_version"`
	DefinitionRevision string                `json:"definition_revision"`
	Concurrency        ConcurrencyPolicy     `json:"concurrency,omitempty"`
	SideEffect         SideEffectPolicy      `json:"side_effect,omitempty"`
	Security           toolpolicy.Capability `json:"security"`
}

type SideEffectMode string

const (
	SideEffectNone     SideEffectMode = "none"
	SideEffectExternal SideEffectMode = "external"
)

// SideEffectPolicy is explicit because external writes must use the durable
// idempotency journal. The zero value is a replay-safe, read-only computation.
type SideEffectPolicy struct {
	Mode SideEffectMode `json:"mode,omitempty"`
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
type ScopeResolver func(ctx context.Context, args json.RawMessage) (toolpolicy.Scope, error)

type ExecutionPolicy struct {
	Timeout        time.Duration
	MaxResultBytes int
}

type Binding struct {
	Descriptor   Descriptor
	Handler      Handler
	Policy       ExecutionPolicy
	ResolveScope ScopeResolver
	contract     *argumentContract
}

type ToolInfo struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
	Enabled     bool           `json:"enabled"`
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
