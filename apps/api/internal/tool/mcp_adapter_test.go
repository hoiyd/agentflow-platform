package tool

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/tool/policy"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func mcpTestConfig(t *testing.T, servers ...MCPServerConfig) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mcp.json")
	data, err := json.Marshal(map[string]any{"servers": servers})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func mcpTestServer(t *testing.T, handler mcp.ToolHandler) (*mcp.Server, *httptest.Server) {
	t.Helper()
	server := mcp.NewServer(&mcp.Implementation{Name: "trusted-docs-fixture", Version: "1"}, nil)
	server.AddTool(&mcp.Tool{Name: "read", Description: "Read a public document", InputSchema: ObjectSchema(map[string]any{"id": map[string]any{"type": "string"}}, []string{"id"})}, handler)
	httpServer := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, PropagateRequestCancellation: true}))
	t.Cleanup(httpServer.Close)
	return server, httpServer
}

func mcpTestAdapter(t *testing.T, endpoint string) *MCPAdapter {
	t.Helper()
	a, err := NewMCPAdapter(mcpTestConfig(t, MCPServerConfig{ID: "docs", URL: endpoint, ReadOnlyTools: []string{"read"}}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	a.Refresh(t.Context())
	return a
}

func mcpTestExecutor(t *testing.T, a *MCPAdapter, allow bool) *Executor {
	t.Helper()
	bindings := a.Bindings()
	p := policy.DefaultPolicy()
	if allow {
		for _, binding := range bindings {
			p.Rules = append(p.Rules, policy.Rule{ID: binding.Descriptor.Name, Tool: binding.Descriptor.Name, Action: policy.ActionAllow, Capability: binding.Descriptor.Security})
		}
	}
	catalog, err := NewCatalogWithPolicy(p, bindings...)
	if err != nil {
		t.Fatal(err)
	}
	return NewExecutor(catalog, ExecutorOptions{})
}

func TestMCPAdapterExecutorBoundaries(t *testing.T) {
	var calls atomic.Int32
	_, remote := mcpTestServer(t, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		calls.Add(1)
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "public evidence"}}, StructuredContent: map[string]any{"title": "Public guide"}}, nil
	})
	a := mcpTestAdapter(t, remote.URL)
	request := ExecutionRequest{Tool: "mcp_docs__read", Arguments: json.RawMessage(`{"id":"guide"}`)}
	denied := mcpTestExecutor(t, a, false).Execute(t.Context(), request)
	if denied.Error == nil || denied.Error.Code != ErrorSecurityPolicyDenied || calls.Load() != 0 {
		t.Fatalf("remote tool bypassed local policy: %+v calls=%d", denied, calls.Load())
	}
	executor := mcpTestExecutor(t, a, true)
	invalid := request
	invalid.Arguments = json.RawMessage(`{"id":12}`)
	if result := executor.Execute(t.Context(), invalid); result.Error == nil || result.Error.Code != ErrorInvalidArgs || calls.Load() != 0 {
		t.Fatalf("invalid input reached server: %+v", result)
	}
	result := executor.Execute(t.Context(), request)
	if result.Error != nil || calls.Load() != 1 {
		t.Fatalf("call: %+v", result)
	}
	encoded, _ := json.Marshal(result.Result)
	if !strings.Contains(string(encoded), "public evidence") || !strings.Contains(string(encoded), "untrusted_external_content") {
		t.Fatalf("missing normalized evidence: %s", encoded)
	}
	info := executor.catalog.List()[0]
	if info.Source == nil || info.Source.ServerID != "docs" || info.Source.RemoteName != "read" {
		t.Fatalf("source missing: %+v", info)
	}
}

func TestMCPAdapterResultFailures(t *testing.T) {
	for _, tc := range []struct {
		name   string
		result *mcp.CallToolResult
		err    error
		want   ErrorCode
	}{
		{"tool_error", &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "not found"}}}, nil, ErrorExecutionFailed},
		{"rpc_error", nil, errors.New("rpc fixture failure"), ErrorProviderUnavailable},
		{"image", &mcp.CallToolResult{Content: []mcp.Content{&mcp.ImageContent{Data: []byte("not an image"), MIMEType: "image/png"}}}, nil, ErrorResultEncoding},
		{"oversize", &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: strings.Repeat("x", 2<<20)}}}, nil, ErrorProviderUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, remote := mcpTestServer(t, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) { return tc.result, tc.err })
			a := mcpTestAdapter(t, remote.URL)
			result := mcpTestExecutor(t, a, true).Execute(t.Context(), ExecutionRequest{Tool: "mcp_docs__read", Arguments: json.RawMessage(`{"id":"guide"}`)})
			if result.Error == nil || result.Error.Code != tc.want {
				t.Fatalf("want %s: %+v", tc.want, result)
			}
		})
	}
}

func TestMCPAdapterDiscoveryAndFrozenDefinitions(t *testing.T) {
	var calls atomic.Int32
	handler := func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		calls.Add(1)
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "guide"}}}, nil
	}
	server, remote := mcpTestServer(t, handler)
	a := mcpTestAdapter(t, remote.URL)
	old := mcpTestExecutor(t, a, true)
	oldRevision := old.catalog.List()[0].Source.DefinitionHash
	server.AddTool(&mcp.Tool{Name: "read", Description: "Changed contract", InputSchema: ObjectSchema(nil, nil), OutputSchema: ObjectSchema(map[string]any{"title": map[string]any{"type": "string"}}, []string{"title"})}, handler)
	request := ExecutionRequest{Tool: "mcp_docs__read", Arguments: json.RawMessage(`{"id":"guide"}`)}
	if result := old.Execute(t.Context(), request); result.Error == nil || result.Error.Code != ErrorExecutionUnavailable || calls.Load() != 0 {
		t.Fatalf("stale definition executed: %+v", result)
	}
	a.Refresh(t.Context())
	current := mcpTestExecutor(t, a, true)
	if current.catalog.List()[0].Source.DefinitionHash == oldRevision {
		t.Fatal("remote change did not change revision")
	}
	if old.catalog.List()[0].Source.DefinitionHash != oldRevision {
		t.Fatal("frozen catalog mutated")
	}
	request.Arguments = json.RawMessage(`{}`)
	if result := current.Execute(t.Context(), request); result.Error == nil || result.Error.Code != ErrorResultEncoding {
		t.Fatalf("missing structured output passed: %+v", result)
	}
	server.AddTool(&mcp.Tool{Name: "read", InputSchema: map[string]any{"type": "object", "$ref": "https://example.com/schema"}}, handler)
	a.Refresh(t.Context())
	if got := a.Bindings()[0].UnavailableReason; got != "mcp_schema_unsupported" {
		t.Fatalf("bad schema availability=%s", got)
	}
}

func TestMCPAdapterUnavailableAndNamespaces(t *testing.T) {
	_, remote := mcpTestServer(t, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{}, nil
	})
	t.Setenv("MCP_FIXTURE_MISSING_KEY", "")
	a, err := NewMCPAdapter(mcpTestConfig(t,
		MCPServerConfig{ID: "one", URL: remote.URL, ReadOnlyTools: []string{"read"}},
		MCPServerConfig{ID: "two", URL: remote.URL, ReadOnlyTools: []string{"read"}, BearerTokenEnv: "MCP_FIXTURE_MISSING_KEY"},
	))
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if a.Bindings()[0].UnavailableReason != "mcp_discovery_pending" {
		t.Fatal("undiscovered tool appeared ready")
	}
	a.Refresh(t.Context())
	bindings := a.Bindings()
	if bindings[0].Descriptor.Name != "mcp_one__read" || bindings[1].Descriptor.Name != "mcp_two__read" || bindings[1].UnavailableReason != "credential_unavailable" {
		t.Fatalf("names/readiness: %+v", bindings)
	}
	remote.Close()
	a.Refresh(t.Context())
	if a.Bindings()[0].UnavailableReason != "mcp_connection_failed" {
		t.Fatal("disconnect did not clear readiness")
	}
}

func TestMCPAdapterCancellation(t *testing.T) {
	started := make(chan struct{})
	_, remote := mcpTestServer(t, func(ctx context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	a := mcpTestAdapter(t, remote.URL)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan ExecutionResult, 1)
	go func() {
		done <- mcpTestExecutor(t, a, true).Execute(ctx, ExecutionRequest{Tool: "mcp_docs__read", Arguments: json.RawMessage(`{"id":"guide"}`)})
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("call did not start")
	}
	cancel()
	select {
	case result := <-done:
		if result.Error == nil || result.Error.Code != ErrorExecutionCanceled {
			t.Fatalf("cancel: %+v", result)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("call ignored cancellation")
	}
}

func TestMCPAdapterRejectsInvalidConfiguration(t *testing.T) {
	for _, servers := range [][]MCPServerConfig{
		{{ID: "w1", URL: "https://example.com/mcp", ReadOnlyTools: []string{"read"}}},
		{{ID: "docs", URL: "http://example.com/mcp", ReadOnlyTools: []string{"read"}}},
		{{ID: "docs", URL: "https://example.com/mcp?key=secret", ReadOnlyTools: []string{"read"}}},
		{{ID: "docs", URL: "https://example.com/mcp", ReadOnlyTools: []string{"read"}}, {ID: "docs", URL: "https://example.com/mcp", ReadOnlyTools: []string{"read"}}},
		{{ID: "docs", URL: "https://example.com/mcp", ReadOnlyTools: []string{"../write"}}},
		{{ID: "docs", URL: "https://example.com/mcp", ReadOnlyTools: []string{"read", "read"}}},
	} {
		if a, err := NewMCPAdapter(mcpTestConfig(t, servers...)); err == nil {
			a.Close()
			t.Fatalf("accepted invalid config: %+v", servers)
		}
	}
}

func TestManagerReplaceBindingsPreservesFrozenCatalog(t *testing.T) {
	manager, err := NewManager("")
	if err != nil {
		t.Fatal(err)
	}
	old, _ := manager.Catalog()
	binding := Binding{Descriptor: Descriptor{Name: "fixture", Description: "fixture", Parameters: ObjectSchema(nil, nil)}, Handler: func(context.Context, json.RawMessage) (any, error) { return "ok", nil }}
	if err := manager.ReplaceBindings([]Binding{binding}); err != nil {
		t.Fatal(err)
	}
	current, _ := manager.Catalog()
	if _, ok := current.Installed("fixture"); !ok {
		t.Fatal("replacement absent")
	}
	if _, ok := old.Installed("fixture"); ok {
		t.Fatal("old catalog mutated")
	}
	if err := manager.ReplaceBindings([]Binding{binding, binding}); err == nil {
		t.Fatal("invalid update accepted")
	}
	if latest, _ := manager.Catalog(); latest != current {
		t.Fatal("failed update changed catalog")
	}
}

func TestMCPReadinessLifecycle(t *testing.T) {
	_, host := mcpTestServer(t, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{}, nil
	})
	a, err := NewMCPAdapter(mcpTestConfig(t, MCPServerConfig{ID: "docs", URL: host.URL, ReadOnlyTools: []string{"read"}, Required: true}))
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if err := a.CheckRequired(t.Context()); err != nil {
		t.Fatal(err)
	}
	if a.Bindings()[0].UnavailableReason != "" {
		t.Fatal("required startup check did not publish validated definitions")
	}
	manager, err := NewManager("", a.Bindings()...)
	if err != nil {
		t.Fatal(err)
	}
	published := make(chan error, 1)
	a.Start(func(bindings []Binding) error {
		err := manager.ReplaceBindings(bindings)
		select {
		case published <- err:
		default:
		}
		return err
	})
	select {
	case err := <-published:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("background discovery did not publish")
	}
	a.Close()
	bindings := a.Bindings()
	if bindings[0].CheckAvailability() != "mcp_adapter_closed" {
		t.Fatal("shutdown did not revoke availability")
	}
	host.Close()
	b, err := NewMCPAdapter(mcpTestConfig(t, MCPServerConfig{ID: "docs", URL: host.URL, ReadOnlyTools: []string{"read"}, Required: true}))
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if b.CheckRequired(t.Context()) == nil {
		t.Fatal("unavailable required server accepted")
	}
}
