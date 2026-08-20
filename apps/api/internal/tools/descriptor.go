package tools

import (
	"context"
	"encoding/json"
	"time"
)

type Descriptor struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Parameters  map[string]any    `json:"parameters"`
	Concurrency ConcurrencyPolicy `json:"concurrency,omitempty"`
	SideEffect  SideEffectPolicy  `json:"side_effect,omitempty"`
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

type ExecutionPolicy struct {
	Timeout        time.Duration
	MaxResultBytes int
}

type Binding struct {
	Descriptor Descriptor
	Handler    Handler
	Policy     ExecutionPolicy
}

type ToolInfo struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
	Enabled     bool           `json:"enabled"`
}

func ObjectSchema(properties map[string]any, required []string) map[string]any {
	return map[string]any{
		"type":                 "object",
		"properties":           properties,
		"required":             required,
		"additionalProperties": false,
	}
}
