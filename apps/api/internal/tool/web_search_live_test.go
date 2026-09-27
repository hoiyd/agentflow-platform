package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentflow-platform/apps/api/internal/credential"
)

func TestWebSearchLiveTavily(t *testing.T) {
	if os.Getenv("TAVILY_LIVE_TEST") != "1" {
		t.Skip("set TAVILY_LIVE_TEST=1 to spend a Tavily search credit")
	}
	key := credential.FromEnvironment("TAVILY_API_KEY")
	if !key.Available() {
		t.Fatal("TAVILY_API_KEY is required for the live test")
	}
	client, err := NewTavilyClient(key)
	if err != nil {
		t.Fatal(err)
	}
	config := DefaultConfig()
	path := filepath.Join(t.TempDir(), "tools.json")
	if err := SaveConfig(path, config); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(path, WebSearchTool(client))
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := manager.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	result := NewExecutor(catalog, ExecutorOptions{CredentialScopes: []string{TavilyCredentialScope}}).Execute(context.Background(), ExecutionRequest{
		Tool: "web_search", Arguments: json.RawMessage(`{"query":"Go programming language official website","max_results":1}`),
	})
	if result.Error != nil {
		t.Fatalf("live search failed: code=%s message=%s", result.Error.Code, result.Error.Message)
	}
	output, ok := result.Result.(webSearchOutput)
	if !ok || len(output.Results) == 0 || output.Results[0].URL == "" {
		t.Fatalf("live search returned no normalized result: type=%T", result.Result)
	}
	encoded, err := json.Marshal(result)
	if err != nil || strings.Contains(string(encoded), key.Reveal()) {
		t.Fatal("live result could not be encoded safely")
	}
	t.Logf("live search succeeded with %d result(s)", len(output.Results))
}
