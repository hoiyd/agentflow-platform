package tools

import (
	"context"
	"encoding/json"
	"time"
)

type Descriptor struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
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
