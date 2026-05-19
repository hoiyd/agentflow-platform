package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

var ErrMCPClientUnavailable = errors.New("mcp client is not available")

type BuildOptions struct {
	Config    Config
	MCPClient MCPClient
}

func BuildRegistry(ctx context.Context, options BuildOptions) (*Registry, error) {
	registry, err := NewRegistry(
		CalculatorTool(),
		CurrentTimeTool(),
		MockWebSearchTool(),
	)
	if err != nil {
		return nil, err
	}

	enabled := map[string]bool{}
	for _, name := range options.Config.EnabledTools {
		name = strings.TrimSpace(name)
		if name != "" {
			enabled[name] = true
		}
	}

	mcpClient := options.MCPClient
	if mcpClient == nil {
		mcpClient = NoopMCPClient{}
	}
	for _, server := range options.Config.MCPServers {
		if !server.Enabled {
			continue
		}
		if strings.TrimSpace(server.ID) == "" {
			return nil, errors.New("enabled mcp server id is required")
		}
		mcpTools, err := mcpClient.ListTools(ctx, server)
		if err != nil {
			return nil, fmt.Errorf("list mcp tools from %q: %w", server.ID, err)
		}
		for _, mcpTool := range mcpTools {
			tool, err := toolFromMCP(server.ID, mcpTool, mcpClient)
			if err != nil {
				return nil, fmt.Errorf("register mcp tool from %q: %w", server.ID, err)
			}
			if err := registry.Register(tool); err != nil {
				return nil, err
			}
		}
	}

	if err := applyEnabledTools(registry, enabled); err != nil {
		return nil, err
	}

	return registry, nil
}

func applyEnabledTools(registry *Registry, enabled map[string]bool) error {
	if len(enabled) == 0 {
		for _, item := range registry.List() {
			if err := registry.SetEnabled(item.Name, false); err != nil {
				return err
			}
		}
		return nil
	}

	known := map[string]bool{}
	for _, item := range registry.List() {
		known[item.Name] = true
		if err := registry.SetEnabled(item.Name, enabled[item.Name]); err != nil {
			return err
		}
	}
	for name := range enabled {
		if !known[name] {
			return fmt.Errorf("enabled tool %q is not installed", name)
		}
	}
	return nil
}

func toolFromMCP(serverID string, mcpTool MCPTool, client MCPClient) (Tool, error) {
	if strings.TrimSpace(mcpTool.Name) == "" {
		return Tool{}, errors.New("mcp tool name is required")
	}
	if mcpTool.Parameters == nil {
		return Tool{}, fmt.Errorf("mcp tool %q parameters are required", mcpTool.Name)
	}
	if _, err := json.Marshal(mcpTool.Parameters); err != nil {
		return Tool{}, fmt.Errorf("mcp tool %q parameters must be json-compatible: %w", mcpTool.Name, err)
	}

	name := mcpTool.Name
	return Tool{
		Name:        name,
		Description: mcpTool.Description,
		Parameters:  mcpTool.Parameters,
		Source:      "mcp",
		SourceID:    serverID,
		Handler: func(ctx context.Context, args json.RawMessage) (any, error) {
			return client.CallTool(ctx, serverID, name, args)
		},
	}, nil
}
