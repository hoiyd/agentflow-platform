package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPCredentialDestinationAndReflection(t *testing.T) {
	const secret = "fixture-mcp-secret-unique-canary"
	t.Setenv("MCP_FIXTURE_TOKEN", secret)
	_, server := mcpTestServer(t, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "reflected " + secret}}, StructuredContent: map[string]any{"note": secret}}, nil
	})
	var received atomic.Int32
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+secret {
			t.Error("missing scoped bearer")
		}
		received.Add(1)
		server.Config.Handler.ServeHTTP(w, r)
	}))
	defer host.Close()
	a, err := NewMCPAdapter(mcpTestConfig(t, MCPServerConfig{ID: "docs", URL: host.URL, ReadOnlyTools: []string{"read"}, BearerTokenEnv: "MCP_FIXTURE_TOKEN"}))
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	a.Refresh(t.Context())
	bindings := a.Bindings()
	bindingsJSON, _ := json.Marshal(bindings[0].Descriptor)
	if strings.Contains(string(bindingsJSON), secret) || strings.Contains(string(bindingsJSON), "MCP_FIXTURE_TOKEN") {
		t.Fatal("credential reference/value escaped into descriptor")
	}
	catalog, err := NewCatalogWithPolicy(mcpTestExecutor(t, a, true).catalog.SecurityPolicy(), bindings...)
	if err != nil {
		t.Fatal(err)
	}
	executor := NewExecutor(catalog, ExecutorOptions{CredentialScopes: a.CredentialScopes()})
	result := executor.Execute(t.Context(), ExecutionRequest{Tool: "mcp_docs__read", Arguments: json.RawMessage(`{"id":"guide"}`)})
	data, _ := json.Marshal(result)
	if result.Error != nil || received.Load() == 0 || strings.Contains(string(data), secret) {
		t.Fatalf("unsafe result: %s", data)
	}
	var redirected atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected.Add(1); w.WriteHeader(200) }))
	defer destination.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	b, err := NewMCPAdapter(mcpTestConfig(t, MCPServerConfig{ID: "docs", URL: redirect.URL, ReadOnlyTools: []string{"read"}, BearerTokenEnv: "MCP_FIXTURE_TOKEN"}))
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	b.Refresh(t.Context())
	if redirected.Load() != 0 || b.Bindings()[0].UnavailableReason != "mcp_connection_failed" {
		t.Fatal("redirect followed or readiness fabricated")
	}
}

func TestMCPDiscoveryRejectsOversizedAndUnsupportedDefinitions(t *testing.T) {
	for _, schema := range []map[string]any{
		{"type": "object", "$schema": "http://json-schema.org/draft-07/schema#"},
		{"type": "object", "description": strings.Repeat("x", MaximumToolSchemaBytes+1)},
	} {
		server, host := mcpTestServer(t, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{}, nil
		})
		server.AddTool(&mcp.Tool{Name: "read", InputSchema: schema}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			t.Error("invalid schema invoked")
			return nil, nil
		})
		a := mcpTestAdapter(t, host.URL)
		if a.Bindings()[0].UnavailableReason != "mcp_schema_unsupported" {
			t.Fatal("unsupported schema appeared ready")
		}
	}
	server, host := mcpTestServer(t, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{}, nil
	})
	for index := 0; index < 130; index++ {
		server.AddTool(&mcp.Tool{Name: fmt.Sprintf("read_%d", index), InputSchema: ObjectSchema(nil, nil)}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) { return nil, nil })
	}
	a := mcpTestAdapter(t, host.URL)
	if a.Bindings()[0].UnavailableReason != "mcp_discovery_failed" {
		t.Fatal("unbounded discovery accepted")
	}
}

func TestMCPBoundedDiscoveryAndClosedAdapter(t *testing.T) {
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.Copy(io.Discard, r.Body); <-r.Context().Done() }))
	defer host.Close()
	a, err := NewMCPAdapter(mcpTestConfig(t, MCPServerConfig{ID: "docs", URL: host.URL, ReadOnlyTools: []string{"read"}}))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	a.Refresh(ctx)
	if time.Since(start) > time.Second || a.Bindings()[0].UnavailableReason != "mcp_connection_failed" {
		t.Fatal("discovery ignored deadline")
	}
	a.Close()
	a.Refresh(t.Context())
	if a.Bindings()[0].CheckAvailability() == "" {
		t.Fatal("closed adapter remained ready")
	}
}

func TestMCPRejectedAuthenticationIsNotReady(t *testing.T) {
	t.Setenv("MCP_REJECTED_TOKEN", "fixture-only-rejected-token")
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "PRIVATE_AUTH_RESPONSE", http.StatusUnauthorized)
	}))
	defer host.Close()
	a, err := NewMCPAdapter(mcpTestConfig(t, MCPServerConfig{ID: "docs", URL: host.URL, ReadOnlyTools: []string{"read"}, BearerTokenEnv: "MCP_REJECTED_TOKEN"}))
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	a.Refresh(t.Context())
	if reason := a.Bindings()[0].UnavailableReason; reason != "mcp_authentication_failed" {
		t.Fatalf("authentication rejection reason=%s", reason)
	}
}

func TestMCPRejectsNegotiatedProtocolDrift(t *testing.T) {
	var calls atomic.Int32
	_, server := mcpTestServer(t, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		calls.Add(1)
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "guide"}}}, nil
	})
	var legacy atomic.Bool
	legacy.Store(true)
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var rpc struct {
			Method string `json:"method"`
		}
		json.Unmarshal(body, &rpc)
		if legacy.Load() && rpc.Method == "server/discover" {
			http.Error(w, "legacy protocol", http.StatusNotFound)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		server.Config.Handler.ServeHTTP(w, r)
	}))
	defer host.Close()
	a := mcpTestAdapter(t, host.URL)
	executor := mcpTestExecutor(t, a, true)
	legacy.Store(false)
	result := executor.Execute(t.Context(), ExecutionRequest{Tool: "mcp_docs__read", Arguments: json.RawMessage(`{"id":"guide"}`)})
	if result.Error == nil || result.Error.Code != ErrorExecutionUnavailable || calls.Load() != 0 {
		t.Fatalf("negotiated protocol silently changed: result=%+v calls=%d", result, calls.Load())
	}
}

func TestMCPLiveDeepWiki(t *testing.T) {
	if os.Getenv("MCP_LIVE_TEST") != "1" {
		t.Skip("opt-in public read; external availability is not a unit-test dependency")
	}
	a, err := NewMCPAdapter(mcpTestConfig(t, MCPServerConfig{ID: "deepwiki", URL: "https://mcp.deepwiki.com/mcp", ReadOnlyTools: []string{"read_wiki_structure"}}))
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	a.Refresh(t.Context())
	result := mcpTestExecutor(t, a, true).Execute(t.Context(), ExecutionRequest{Tool: "mcp_deepwiki__read_wiki_structure", Arguments: json.RawMessage(`{"repoName":"modelcontextprotocol/go-sdk"}`)})
	if result.Error != nil {
		t.Fatalf("live MCP read failed: %+v", result.Error)
	}
	data, err := json.MarshalIndent(map[string]any{"schema": "trusted-mcp-live-v1", "transport": "streamable_http", "endpoint": "https://mcp.deepwiki.com/mcp", "repository": "modelcontextprotocol/go-sdk", "timestamp": time.Now().UTC(), "source": a.Bindings()[0].Descriptor.Source, "result": result, "limitations": []string{"public repository read only", "no live LLM task", "external availability varies"}}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := os.Getenv("MCP_EVIDENCE_PATH")
	if path == "" {
		t.Fatal("MCP_EVIDENCE_PATH is required to retain repeatable live evidence")
	}
	if err := os.WriteFile(path, append(data, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	t.Logf("retained live evidence: %s", path)
}
