package httpapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/mcpconnection"
	"agentflow-platform/apps/api/internal/tool"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Failure inventory extends the lifecycle gate: invalid intent must not persist;
// missing/wrong/corrupted keys disable only the affected dependency; operator
// denies win; archived Workspaces reject commands; storage errors remain errors.
func TestWorkspaceMCPConnectionFaultBoundaries(t *testing.T) {
	h, storage, db, spaces, tokens := authorizationFixture(t)
	remote := mcp.NewServer(&mcp.Implementation{Name: "fault-docs", Version: "1"}, nil)
	remote.AddTool(&mcp.Tool{Name: "read", Description: "Read bounded evidence", InputSchema: tool.ObjectSchema(nil, nil)}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "Evidence"}}}, nil
	})
	host := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return remote }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true}))
	t.Cleanup(host.Close)
	key := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))
	service, err := mcpconnection.New(storage, mcpconnection.Config{EncryptionKey: key, LocalOrigins: []string{host.URL}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.Close)
	h.mcpConnections = service
	routes := h.Routes()
	request := func(method, path string, body any, want int) map[string]any {
		t.Helper()
		encoded, _ := json.Marshal(body)
		response := authorizedRequest(routes, method, path, spaces[0], tokens[0], string(encoded))
		if response.Code != want {
			t.Fatalf("%s %s got %d want %d: %s", method, path, response.Code, want, response.Body.String())
		}
		var output map[string]any
		if response.Body.Len() > 0 && response.Body.Bytes()[0] == '{' {
			if err := json.Unmarshal(response.Body.Bytes(), &output); err != nil {
				t.Fatal(err)
			}
		}
		return output
	}
	base := func() map[string]any {
		return map[string]any{"name": "Docs", "url": host.URL, "auth_mode": "none", "enabled": false, "read_only_tools": []string{}}
	}
	for _, modify := range []func(map[string]any){
		func(v map[string]any) { v["name"] = "" }, func(v map[string]any) { v["name"] = strings.Repeat("n", 81) },
		func(v map[string]any) { v["name"] = "token=private-value" }, func(v map[string]any) { v["url"] = "https://x.example/mcp#secret" },
		func(v map[string]any) { v["auth_mode"] = "oauth" }, func(v map[string]any) { v["auth_mode"] = "bearer" },
		func(v map[string]any) { v["url"] = "https://service.example/token=private-value" },
		func(v map[string]any) { v["auth_mode"] = "bearer"; v["bearer_token"] = "value\r\nheader" },
		func(v map[string]any) { v["bearer_token"] = "unexpected" }, func(v map[string]any) { v["enabled"] = true },
		func(v map[string]any) { v["read_only_tools"] = []string{"unknown"} },
		func(v map[string]any) {
			names := make([]string, 65)
			for i := range names {
				names[i] = strings.Repeat("n", i+1)
			}
			v["read_only_tools"] = names
		},
		func(v map[string]any) { v["workspace_id"] = spaces[1] },
	} {
		input := base()
		modify(input)
		request("POST", "/api/mcp/connections", input, 400)
	}
	for _, id := range []string{"0", "invalid", "9223372036854775808"} {
		request("GET", "/api/mcp/connections/"+id, nil, 404)
	}
	for _, bad := range []string{`{} {}`, strings.Repeat("x", 70<<10)} {
		response := authorizedRequest(routes, "POST", "/api/mcp/connections", spaces[0], tokens[0], bad)
		if response.Code != 400 {
			t.Fatalf("malformed body: %d", response.Code)
		}
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM mcp_connections`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("invalid input persisted: %d %v", count, err)
	}
	for _, cfg := range []mcpconnection.Config{{EncryptionKey: "not-base64"}, {LocalOrigins: []string{"https://service.example/path"}}} {
		if s, err := mcpconnection.New(storage, cfg); err == nil {
			s.Close()
			t.Fatal("invalid operator configuration accepted")
		}
	}
	withoutKey, err := mcpconnection.New(storage, mcpconnection.Config{LocalOrigins: []string{host.URL}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(withoutKey.Close)
	h.mcpConnections = withoutKey
	routes = h.Routes()
	input := base()
	input["auth_mode"], input["bearer_token"] = "bearer", "test-credential"
	request("POST", "/api/mcp/connections", input, 503)
	h.mcpConnections = service
	routes = h.Routes()
	created := request("POST", "/api/mcp/connections", input, 201)
	id := created["id"].(string)
	path := "/api/mcp/connections/" + id
	discovered := request("POST", path+"/test", nil, 200)
	input = base()
	input["auth_mode"], input["revision"], input["enabled"], input["read_only_tools"] = "bearer", discovered["revision"], true, []string{"read"}
	request("PATCH", path, input, 200)
	response := authorizedRequest(routes, "GET", "/api/mcp/connections", spaces[0], tokens[0], "")
	if response.Code != 200 || strings.Contains(response.Body.String(), "test-credential") {
		t.Fatalf("unsafe list: %s", response.Body.String())
	}
	// Losing or using a different deployment key must not fail unrelated Chat.
	wrongKey, err := mcpconnection.New(storage, mcpconnection.Config{EncryptionKey: base64.StdEncoding.EncodeToString([]byte(strings.Repeat("x", 32))), LocalOrigins: []string{host.URL}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(wrongKey.Close)
	for _, s := range []*mcpconnection.Service{withoutKey, wrongKey} {
		catalog, err := s.Catalog(spaces[0], tool.DefaultCatalog())
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := catalog.ResolveReady("calculator"); !ok {
			t.Fatal("optional credential failure broke built-in tools")
		}
		if _, ok := catalog.ResolveReady("mcp_w" + id + "__read"); ok {
			t.Fatal("invalid key retained ready MCP binding")
		}
		if _, err := s.Test(t.Context(), spaces[0], id); err == nil {
			t.Fatal("invalid key accepted during test")
		}
	}
	if _, err := db.Exec(`UPDATE mcp_connections SET credential_ciphertext='broken' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Test(t.Context(), spaces[0], id); err == nil {
		t.Fatal("corrupted ciphertext accepted")
	}
	request("DELETE", path, nil, 204)
	// Empty auth works without the encryption key, and clearing auth erases data.
	h.mcpConnections = withoutKey
	routes = h.Routes()
	created = request("POST", "/api/mcp/connections", base(), 201)
	if created["credential_configured"] != false {
		t.Fatal("unauthenticated connection retained a token")
	}
	path = "/api/mcp/connections/" + created["id"].(string)
	if _, err := db.Exec(`UPDATE workspaces SET status='archived' WHERE id=$1`, spaces[0]); err != nil {
		t.Fatal(err)
	}
	request("POST", path+"/test", nil, 409)
	request("DELETE", path, nil, 409)
	request("PATCH", path, base(), 409)
	if _, err := service.Save(t.Context(), spaces[0], "", mcpconnection.Input{}); err == nil {
		t.Fatal("service bypassed Workspace lifecycle")
	}
	if _, err := service.Test(t.Context(), spaces[0], created["id"].(string)); err == nil {
		t.Fatal("test bypassed Workspace lifecycle")
	}
	if err := service.Delete(t.Context(), spaces[0], created["id"].(string)); err == nil {
		t.Fatal("delete bypassed Workspace lifecycle")
	}
	if _, err := db.Exec(`ALTER TABLE mcp_connections RENAME TO unavailable_mcp_connections`); err != nil {
		t.Fatal(err)
	}
	request("GET", "/api/mcp/connections", nil, 500)
	if _, err := service.Catalog(spaces[0], tool.DefaultCatalog()); err == nil {
		t.Fatal("storage failure became successful catalog")
	}
}

// Tests use protocol barriers, not sleeps: stale discovery cannot overwrite an
// edit, capacity is bounded, and revocation during setup prevents tools/call.
func TestWorkspaceMCPConnectionDiscoveryRaceAndCallRevocation(t *testing.T) {
	h, storage, _, spaces, tokens := authorizationFixture(t)
	entered := make(chan struct{}, 4)
	release := make(chan struct{})
	var block, revoke atomic.Bool
	defer func() {
		block.Store(false)
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	var calls atomic.Int32
	var revokeConnection func(context.Context) error
	remote := mcp.NewServer(&mcp.Implementation{Name: "barrier-docs", Version: "1"}, nil)
	remote.AddTool(&mcp.Tool{Name: "read", InputSchema: tool.ObjectSchema(nil, nil)}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		calls.Add(1)
		return &mcp.CallToolResult{}, nil
	})
	protocol := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return remote }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, PropagateRequestCancellation: true})
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if block.Load() {
			entered <- struct{}{}
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(400)
			return
		}
		r.Body = io.NopCloser(strings.NewReader(string(body)))
		var rpc struct {
			Method string `json:"method"`
		}
		_ = json.Unmarshal(body, &rpc)
		if rpc.Method == "tools/list" && revoke.CompareAndSwap(true, false) {
			if err := revokeConnection(r.Context()); err != nil {
				w.WriteHeader(500)
				return
			}
		}
		protocol.ServeHTTP(w, r)
	}))
	t.Cleanup(host.Close)
	service, err := mcpconnection.New(storage, mcpconnection.Config{LocalOrigins: []string{host.URL}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.Close)
	h.mcpConnections = service
	input := mcpconnection.Input{Name: "Barrier docs", URL: host.URL, AuthMode: "none"}
	created, err := service.Save(t.Context(), spaces[0], "", input)
	if err != nil {
		t.Fatal(err)
	}
	routes := h.Routes()
	path := "/api/mcp/connections/" + created.ID + "/test"
	block.Store(true)
	responses := make(chan *httptest.ResponseRecorder, 4)
	for range 4 {
		go func() { responses <- authorizedRequest(routes, "POST", path, spaces[0], tokens[0], "") }()
	}
	for range 4 {
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("discovery requests did not reach the protocol barrier")
		}
	}
	fifth := authorizedRequest(routes, "POST", path, spaces[0], tokens[0], "")
	if fifth.Code != 429 || fifth.Header().Get("Retry-After") == "" {
		t.Fatalf("unbounded discovery: %d %s", fifth.Code, fifth.Body.String())
	}
	input.Name, input.Revision = "Edited while testing", created.Revision
	if _, err := service.Save(t.Context(), spaces[0], created.ID, input); err != nil {
		t.Fatal(err)
	}
	block.Store(false)
	close(release)
	for range 4 {
		select {
		case response := <-responses:
			if response.Code != 409 {
				t.Fatalf("stale discovery result: %d %s", response.Code, response.Body.String())
			}
		case <-time.After(5 * time.Second):
			t.Fatal("discovery did not finish")
		}
	}
	current, err := service.Get(spaces[0], created.ID)
	if err != nil || current.Name != input.Name || current.Status != "configured" {
		t.Fatalf("stale result replaced owner intent: %#v %v", current, err)
	}
	discovered, err := service.Test(t.Context(), spaces[0], created.ID)
	if err != nil {
		t.Fatal(err)
	}
	input.Revision, input.Enabled, input.ReadOnlyTools = discovered.Revision, true, []string{"read"}
	enabled, err := service.Save(t.Context(), spaces[0], created.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := service.Catalog(spaces[0], tool.DefaultCatalog())
	if err != nil {
		t.Fatal(err)
	}
	binding, ok := catalog.ResolveReady("mcp_w" + created.ID + "__read")
	if !ok {
		t.Fatal("selected binding missing")
	}
	revokeConnection = func(ctx context.Context) error {
		input.Revision, input.Enabled = enabled.Revision, false
		_, err := service.Save(ctx, spaces[0], created.ID, input)
		return err
	}
	revoke.Store(true)
	if _, err := binding.Handler(t.Context(), json.RawMessage(`{}`)); err == nil || calls.Load() != 0 {
		t.Fatalf("revocation during setup reached tools/call: calls=%d err=%v", calls.Load(), err)
	}
}
