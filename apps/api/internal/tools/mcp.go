package tools

import (
	"context"
	"encoding/json"
)

type MCPClient interface {
	ListTools(ctx context.Context, server MCPServerConfig) ([]MCPTool, error)
	CallTool(ctx context.Context, serverID string, name string, args json.RawMessage) (any, error)
}

type MCPTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type NoopMCPClient struct{}

func (NoopMCPClient) ListTools(ctx context.Context, server MCPServerConfig) ([]MCPTool, error) {
	return nil, nil
}

func (NoopMCPClient) CallTool(ctx context.Context, serverID string, name string, args json.RawMessage) (any, error) {
	return nil, ErrMCPClientUnavailable
}
