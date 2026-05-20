package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestStdioMCPClientWithExternalServer(t *testing.T) {
	if os.Getenv("AGENTFLOW_RUN_EXTERNAL_MCP") == "" {
		t.Skip("set AGENTFLOW_RUN_EXTERNAL_MCP=1 to run the external MCP server integration test")
	}
	npx, err := findNPX()
	if err != nil {
		t.Skip("npx is not installed; skipping external MCP server integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client := NewStdioMCPClient()
	defer func() {
		if err := client.Close(); err != nil {
			t.Fatalf("close mcp client: %v", err)
		}
	}()

	server := MCPServerConfig{
		ID:        "filesystem",
		Enabled:   true,
		Transport: "stdio",
		Command:   npx,
		Args:      []string{"-y", "@modelcontextprotocol/server-filesystem", t.TempDir()},
		Env:       map[string]string{"PATH": filepath.Dir(npx) + string(os.PathListSeparator) + os.Getenv("PATH")},
	}

	listed, err := client.ListTools(ctx, server)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(listed) < 5 {
		t.Fatalf("expected at least 5 filesystem tools, got %d: %#v", len(listed), listed)
	}
	for _, name := range []string{"read_text_file", "write_file", "list_directory", "create_directory", "search_files"} {
		if !hasMCPTool(listed, name) {
			t.Fatalf("expected filesystem tool %q, got %#v", name, listed)
		}
	}

	result, err := client.CallTool(ctx, "filesystem", "list_allowed_directories", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("call list_allowed_directories: %v", err)
	}
	payload, ok := result.(mcpCallToolResult)
	if !ok {
		t.Fatalf("unexpected mcp result type %T", result)
	}
	if len(payload.Content) == 0 {
		t.Fatal("expected external mcp server content")
	}
}

func TestHTTPMCPClientWithSmartAPIs(t *testing.T) {
	if os.Getenv("AGENTFLOW_RUN_EXTERNAL_MCP") == "" {
		t.Skip("set AGENTFLOW_RUN_EXTERNAL_MCP=1 to run the external MCP server integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client := NewHTTPMCPClient()
	server := MCPServerConfig{
		ID:        "smartapis",
		Enabled:   true,
		Transport: "streamable-http",
		URL:       "https://smartapis.net/mcp",
	}

	listed, err := client.ListTools(ctx, server)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if !hasMCPTool(listed, "smartagent_discovery_capabilities") {
		t.Fatalf("expected smartagent_discovery_capabilities tool, got %#v", listed)
	}

	result, err := client.CallTool(ctx, "smartapis", "smartagent_discovery_capabilities", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("call smartagent_discovery_capabilities: %v", err)
	}
	payload, ok := result.(mcpCallToolResult)
	if !ok {
		t.Fatalf("unexpected mcp result type %T", result)
	}
	if len(payload.Content) == 0 {
		t.Fatal("expected smartapis mcp server content")
	}
}

func hasMCPTool(tools []MCPTool, name string) bool {
	for _, tool := range tools {
		if tool.Name == name {
			return true
		}
	}
	return false
}

func findNPX() (string, error) {
	if path := os.Getenv("AGENTFLOW_NPX"); path != "" {
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
		return "", errors.New("AGENTFLOW_NPX does not point to an executable file")
	}
	if path, err := exec.LookPath("npx"); err == nil {
		return path, nil
	}
	return "", errors.New("npx is not on PATH")
}
