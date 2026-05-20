package tools

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestBuildRegistryEnablesOnlyConfiguredBuiltinTools(t *testing.T) {
	registry, err := BuildRegistry(context.Background(), BuildOptions{
		Config: Config{EnabledTools: []string{"calculator"}},
	})
	if err != nil {
		t.Fatalf("build registry: %v", err)
	}

	definitions := registry.Definitions()
	if len(definitions) != 1 {
		t.Fatalf("expected 1 definition, got %d", len(definitions))
	}
	fn := definitions[0]["function"].(map[string]any)
	if fn["name"] != "calculator" {
		t.Fatalf("expected calculator definition, got %v", fn["name"])
	}

	result := registry.Execute(context.Background(), "get_current_time", json.RawMessage(`{}`))
	if result.Error == "" {
		t.Fatal("expected disabled tool execution to fail")
	}
}

func TestBuildRegistryRejectsUnknownEnabledTool(t *testing.T) {
	_, err := BuildRegistry(context.Background(), BuildOptions{
		Config: Config{EnabledTools: []string{"missing_tool"}},
	})
	if err == nil {
		t.Fatal("expected unknown enabled tool to fail")
	}
}

func TestBuildRegistryRegistersEnabledMCPTools(t *testing.T) {
	client := &fakeMCPClient{
		tools: []MCPTool{
			{
				Name:        "fake_lookup",
				Description: "Fake lookup tool.",
				Parameters:  ObjectSchema(map[string]any{"query": map[string]any{"type": "string"}}, []string{"query"}),
			},
		},
		result: map[string]any{"ok": true},
	}

	registry, err := BuildRegistry(context.Background(), BuildOptions{
		Config: Config{
			EnabledTools: []string{"fake__fake_lookup"},
			MCPServers: []MCPServerConfig{
				{ID: "fake", Enabled: true, Transport: "stdio"},
			},
		},
		MCPClient: client,
	})
	if err != nil {
		t.Fatalf("build registry: %v", err)
	}

	definitions := registry.Definitions()
	if len(definitions) != 1 {
		t.Fatalf("expected 1 definition, got %d", len(definitions))
	}
	result := registry.Execute(context.Background(), "fake__fake_lookup", json.RawMessage(`{"query":"agentflow"}`))
	if result.Error != "" {
		t.Fatalf("expected mcp tool call to succeed, got %q", result.Error)
	}
	if client.calledServerID != "fake" || client.calledName != "fake_lookup" {
		t.Fatalf("unexpected mcp call target %q/%q", client.calledServerID, client.calledName)
	}
}

func TestBuildRegistryRejectsInvalidMCPTool(t *testing.T) {
	_, err := BuildRegistry(context.Background(), BuildOptions{
		Config: Config{
			EnabledTools: []string{"fake__broken"},
			MCPServers: []MCPServerConfig{
				{ID: "fake", Enabled: true},
			},
		},
		MCPClient: &fakeMCPClient{
			tools: []MCPTool{{Name: "broken"}},
		},
	})
	if err == nil {
		t.Fatal("expected invalid mcp tool to fail")
	}
}

type fakeMCPClient struct {
	tools          []MCPTool
	result         any
	listErr        error
	callErr        error
	calledServerID string
	calledName     string
}

func (f *fakeMCPClient) ListTools(ctx context.Context, server MCPServerConfig) ([]MCPTool, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	if server.ID == "" {
		return nil, errors.New("missing server id")
	}
	return f.tools, nil
}

func (f *fakeMCPClient) CallTool(ctx context.Context, serverID string, name string, args json.RawMessage) (any, error) {
	f.calledServerID = serverID
	f.calledName = name
	if f.callErr != nil {
		return nil, f.callErr
	}
	return f.result, nil
}
