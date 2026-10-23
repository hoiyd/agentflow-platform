package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"agentflow-platform/apps/api/internal/config"
	"agentflow-platform/apps/api/internal/testsupport/modelstream"
	"agentflow-platform/apps/api/internal/tool"
	"agentflow-platform/apps/api/internal/tool/policy"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func browserMCPConfig(t *testing.T, cfg *config.Config, root string) http.Handler {
	t.Helper()
	server := mcp.NewServer(&mcp.Implementation{Name: "public-docs-fixture", Version: "1"}, nil)
	var calls atomic.Int32
	definition := &mcp.Tool{Name: "read", Description: "Read public project documentation", InputSchema: tool.ObjectSchema(map[string]any{"id": map[string]any{"type": "string"}}, []string{"id"}), OutputSchema: tool.ObjectSchema(map[string]any{"title": map[string]any{"type": "string"}}, []string{"title"})}
	handler := func(_ context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		calls.Add(1)
		if string(request.Params.Arguments) == `{"id":"missing"}` {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "Document not found"}}}, nil
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "MCP public documentation evidence."}}, StructuredContent: map[string]any{"title": "Public project guide"}}, nil
	}
	server.AddTool(definition, handler)
	protocol := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, PropagateRequestCancellation: true})
	const workspaceToken = "fixture-workspace-mcp-token"
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if os.Getenv("AGENTFLOW_MCP_CONNECTION_TEST") == "1" && r.Header.Get("Authorization") != "Bearer "+workspaceToken {
			w.WriteHeader(401)
			return
		}
		protocol.ServeHTTP(w, r)
	}))
	t.Cleanup(host.Close)
	if os.Getenv("AGENTFLOW_MCP_CONNECTION_TEST") == "1" {
		cfg.MCPWorkspaceLocalOrigins = host.URL
		t.Setenv("MCP_CREDENTIAL_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32))))
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]any{"endpoint": host.URL, "token": workspaceToken, "calls": calls.Load()})
		})
	}
	t.Setenv("MCP_BROWSER_MISSING_TOKEN", "")
	cfg.MCPConfigPath = filepath.Join(root, "mcp.json")
	data, _ := json.Marshal(map[string]any{"servers": []tool.MCPServerConfig{
		{ID: "docs", URL: host.URL, ReadOnlyTools: []string{"read"}},
		{ID: "offline", URL: host.URL, ReadOnlyTools: []string{"read"}, BearerTokenEnv: "MCP_BROWSER_MISSING_TOKEN"},
	}})
	if err := os.WriteFile(cfg.MCPConfigPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	adapter, err := tool.NewMCPAdapter(cfg.MCPConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	defer adapter.Close()
	adapter.Refresh(t.Context())
	tools := tool.DefaultConfig()
	for _, binding := range adapter.Bindings() {
		tools.EnabledTools = append(tools.EnabledTools, binding.Descriptor.Name)
		tools.SecurityPolicy.Rules = append(tools.SecurityPolicy.Rules, policy.Rule{ID: binding.Descriptor.Name, Tool: binding.Descriptor.Name, Action: policy.ActionAllowAndLog, Capability: binding.Descriptor.Security})
	}
	if err := tool.SaveConfig(cfg.ToolConfigPath, tools); err != nil {
		t.Fatal(err)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			changed := *definition
			changed.Description = "Changed public documentation contract"
			server.AddTool(&changed, handler)
		}
		json.NewEncoder(w).Encode(map[string]any{"calls": calls.Load()})
	})
}

func (f *browserProvider) mcpResponse(w http.ResponseWriter, input browserModelRequest, task string) bool {
	if !strings.Contains(task, "mcp-gate") {
		return false
	}
	content, reason := "MCP evidence confirmed.", "stop"
	var calls []any
	toolName := "mcp_docs__read"
	available, observations := false, 0
	for _, definition := range input.Tools {
		if definition.Function.Name == "mcp_docs__read" || strings.HasPrefix(definition.Function.Name, "mcp_w") && strings.HasSuffix(definition.Function.Name, "__read") {
			available = true
			toolName = definition.Function.Name
		}
	}
	for position, message := range input.Messages {
		for _, call := range message.ToolCalls {
			if call.Function.Name != toolName {
				continue
			}
			next := position + 1
			want := "execution_failed"
			if observations == 1 {
				want = "MCP public documentation evidence"
			}
			if next >= len(input.Messages) || input.Messages[next].Role != "tool" || input.Messages[next].ToolCallID != call.ID || !strings.Contains(input.Messages[next].Content, want) {
				f.reject(w, "MCP continuation lost failure, result or call identity")
				return true
			}
			observations++
		}
	}
	if input.Stream && available && observations < 2 {
		id := "missing"
		if observations == 1 {
			id = "guide"
		}
		content, reason = "", "tool_calls"
		calls = []any{map[string]any{"id": fmt.Sprintf("mcp-%d", observations), "type": "function", "function": map[string]string{"name": toolName, "arguments": fmt.Sprintf(`{"id":%q}`, id)}}}
	} else if input.Stream && !available {
		content = "Plan: read the public project guide and summarize the documentation."
	} else if !input.Stream && strings.Contains(input.Messages[0].Content, "Decide stage") {
		content = `{"decision":"stop","reason":"public documentation read","final_answer":"MCP evidence confirmed."}`
	}
	completion := browserCompletion(content, reason, calls, nil)
	if input.Stream {
		stream, err := modelstream.Completion(completion)
		if err != nil {
			f.reject(w, err.Error())
			return true
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, stream)
	} else {
		json.NewEncoder(w).Encode(completion)
	}
	return true
}
