package httpapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"agentflow-platform/apps/api/internal/mcpconnection"
	"agentflow-platform/apps/api/internal/tool"
	"agentflow-platform/apps/api/internal/tool/policy"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Failure inventory: foreign Workspace IDs must not reveal or mutate connections;
// secrets must survive restart only as ciphertext; failed auth/discovery never
// grants tools; unsafe URLs, stale edits and revoked connections fail closed.
func TestWorkspaceMCPConnectionLifecycle(t *testing.T) {
	h, storage, db, spaces, tokens := authorizationFixture(t)
	secret := "fixture-connection-bearer-secret"
	var calls atomic.Int32
	server := mcp.NewServer(&mcp.Implementation{Name: "workspace-docs", Version: "1"}, nil)
	server.AddTool(&mcp.Tool{Name: "read", Description: "Read reviewed documentation", InputSchema: tool.ObjectSchema(nil, nil)}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		calls.Add(1)
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "Workspace documentation evidence"}}}, nil
	})
	server.AddTool(&mcp.Tool{Name: "delete", Description: "Destructive operation", InputSchema: tool.ObjectSchema(nil, nil), Annotations: &mcp.ToolAnnotations{DestructiveHint: new(true)}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t.Error("write tool invoked")
		return nil, nil
	})
	protocol := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+secret {
			w.WriteHeader(401)
			return
		}
		protocol.ServeHTTP(w, r)
	}))
	t.Cleanup(host.Close)
	cfg := mcpconnection.Config{EncryptionKey: base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32))), LocalOrigins: []string{host.URL}}
	connections, err := mcpconnection.New(storage, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(connections.Close)
	h.mcpConnections = connections
	manager, err := tool.NewManager("")
	if err != nil {
		t.Fatal(err)
	}
	manager.SetWorkspaceResolver(connections.Catalog)
	h.tools = manager
	routes := h.Routes()
	request := func(method, path, workspace, token string, body any, want int) map[string]any {
		t.Helper()
		encoded, _ := json.Marshal(body)
		response := authorizedRequest(routes, method, path, workspace, token, string(encoded))
		if response.Code != want {
			t.Fatalf("%s %s: %d want %d: %s", method, path, response.Code, want, response.Body.String())
		}
		if strings.Contains(response.Body.String(), secret) {
			t.Fatal("credential leaked in API")
		}
		var result map[string]any
		if response.Body.Len() > 0 && response.Body.Bytes()[0] == '{' {
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
		}
		return result
	}
	input := map[string]any{"name": "Documentation", "url": host.URL, "auth_mode": "bearer", "bearer_token": secret, "enabled": false, "read_only_tools": []string{}}
	created := request("POST", "/api/mcp/connections", spaces[0], tokens[0], input, 201)
	id := created["id"].(string)
	path := "/api/mcp/connections/" + id
	for _, action := range []struct{ method, suffix string }{{"GET", ""}, {"POST", "/test"}, {"PATCH", ""}, {"DELETE", ""}} {
		request(action.method, path+action.suffix, spaces[1], tokens[0], input, 404)
		request(action.method, path+action.suffix, spaces[2], tokens[1], input, 404)
	}
	var ciphertext string
	if err := db.QueryRow(`SELECT credential_ciphertext FROM mcp_connections WHERE id=$1`, id).Scan(&ciphertext); err != nil || ciphertext == "" || strings.Contains(ciphertext, secret) {
		t.Fatalf("credential storage: %q %v", ciphertext, err)
	}
	tested := request("POST", path+"/test", spaces[0], tokens[0], nil, 200)
	if tested["status"] != "discovered" {
		t.Fatalf("discovery: %#v", tested)
	}
	name := "mcp_w" + id + "__read"
	input["bearer_token"] = nil
	input["revision"], input["enabled"], input["read_only_tools"] = tested["revision"], true, []string{"read"}
	updated := request("PATCH", path, spaces[0], tokens[0], input, 200)
	request("PATCH", path, spaces[0], tokens[0], input, 409)
	input["revision"], input["read_only_tools"] = updated["revision"], []string{"delete"}
	request("PATCH", path, spaces[0], tokens[0], input, 400)
	input["read_only_tools"] = []string{"read"}
	catalog, err := manager.CatalogForWorkspace(spaces[0])
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := manager.CatalogForWorkspace(spaces[1])
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := foreign.Installed(name); ok {
		t.Fatal("connection leaked through Catalog")
	}
	binding, ok := catalog.ResolveReady(name)
	if !ok {
		t.Fatal("selected tool missing")
	}
	executor := tool.NewExecutor(catalog, tool.ExecutorOptions{CredentialScopes: binding.Descriptor.Security.Scope.Credentials, Tracer: mcpPolicyTrace{}})
	result := executor.Execute(t.Context(), tool.ExecutionRequest{Tool: name, Arguments: json.RawMessage(`{}`)})
	encodedResult, _ := json.Marshal(result.Result)
	if result.Error != nil || !strings.Contains(string(encodedResult), "Workspace documentation evidence") {
		t.Fatalf("real Executor: %#v", result)
	}
	// Reconstruct the management service with the same key: persisted definitions
	// and encrypted credentials remain usable without another manual discovery.
	restarted, err := mcpconnection.New(storage, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restarted.Close)
	if view, err := restarted.Get(spaces[0], id); err != nil || !view.CredentialConfigured {
		t.Fatalf("restart: %#v %v", view, err)
	}
	restartedCatalog, err := restarted.Catalog(spaces[0], tool.DefaultCatalog())
	if err != nil {
		t.Fatal(err)
	}
	restored := tool.NewExecutor(restartedCatalog, tool.ExecutorOptions{CredentialScopes: binding.Descriptor.Security.Scope.Credentials, Tracer: mcpPolicyTrace{}}).Execute(t.Context(), tool.ExecutionRequest{Tool: name, Arguments: json.RawMessage(`{}`)})
	if restored.Error != nil {
		t.Fatalf("restart cannot execute with encrypted token: %v", restored.Error)
	}
	input["name"], input["revision"] = "Renamed service", updated["revision"]
	updated = request("PATCH", path, spaces[0], tokens[0], input, 200)
	if _, err := manager.CatalogForWorkspace(spaces[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := binding.Handler(t.Context(), json.RawMessage(`{}`)); err != nil {
		t.Fatalf("rename invalidated unchanged frozen definition: %v", err)
	}
	input["revision"], input["enabled"] = updated["revision"], false
	disabled := request("PATCH", path, spaces[0], tokens[0], input, 200)
	before := calls.Load()
	if _, err := binding.Handler(t.Context(), json.RawMessage(`{}`)); err == nil || calls.Load() != before {
		t.Fatal("frozen Binding executed after disable")
	}
	input["revision"], input["enabled"], input["bearer_token"] = disabled["revision"], false, "wrong-token"
	input["read_only_tools"] = []string{}
	request("PATCH", path, spaces[0], tokens[0], input, 200)
	failed := request("POST", path+"/test", spaces[0], tokens[0], nil, 200)
	if failed["status"] != "mcp_authentication_failed" {
		t.Fatalf("auth failure: %#v", failed)
	}
	request("DELETE", path, spaces[0], tokens[0], nil, 204)
	request("GET", path, spaces[0], tokens[0], nil, 404)
	if _, err := binding.Handler(t.Context(), json.RawMessage(`{}`)); err == nil || calls.Load() != before {
		t.Fatal("deleted connection executed")
	}
	for _, endpoint := range []string{"http://127.0.0.1:1/mcp", "https://169.254.169.254/mcp", "https://service.example/mcp?token=secret"} {
		input["url"], input["enabled"], input["read_only_tools"] = endpoint, false, []string{}
		request("POST", "/api/mcp/connections", spaces[0], tokens[0], input, 400)
	}
	t.Logf("connection=%s workspace=%s calls=%d; encrypted persistence, auth failure, foreign scopes and revocation verified", id, spaces[0], calls.Load())
}

type mcpPolicyTrace struct{}

func (mcpPolicyTrace) ToolStarted(context.Context, tool.ExecutionRequest) {}
func (mcpPolicyTrace) ToolFinished(context.Context, tool.ExecutionResult) {}
func (mcpPolicyTrace) ToolPolicyEvaluated(context.Context, tool.ExecutionRequest, policy.Decision) error {
	return nil
}
