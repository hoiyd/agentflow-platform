package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/config"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/testsupport/pgfixture"
)

// This gate also runs in ordinary backend CI: browser gates are complementary,
// not the only exercise of the production handler/runtime/transaction wiring.
func TestProductionInboxFinalBoundaryAndDispatch(t *testing.T) {
	databaseURL := pgfixture.DatabaseURL(t)
	root := t.TempDir()
	t.Chdir(root)
	fixture := newBrowserProvider()
	providerServer := httptest.NewServer(http.HandlerFunc(fixture.respond))
	defer providerServer.Close()
	t.Setenv("BROWSER_FIXTURE_KEY", "fixture-only")
	t.Setenv("EMBEDDING_API_KEY", "fixture-only")
	t.Setenv("TAVILY_API_KEY", "")
	route := filepath.Join(root, "routes.json")
	routes, err := json.Marshal(map[string]any{"routes": []any{map[string]any{
		"id": "inbox-fixture", "model": "fixture-model", "base_url": providerServer.URL,
		"credential_environment": "BROWSER_FIXTURE_KEY", "request_timeout_seconds": 20,
		"capabilities":          map[string]bool{"tool_calling": true, "streaming": true, "structured_output": true},
		"context_window_tokens": 128000, "max_output_tokens": 8192, "priority": 100,
		"pricing": map[string]string{"source": "fixture"},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(route, routes, 0600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Load()
	cfg.DatabaseURL, cfg.ModelRouteConfigPath = databaseURL, route
	cfg.ToolConfigPath, cfg.TrustedSkillDirectories = filepath.Join(root, "tools.json"), root
	cfg.AuthMode, cfg.SandboxEnabled = "local", false
	cfg.EmbeddingBaseURL, cfg.EmbeddingModel, cfg.EmbeddingDimensions = providerServer.URL, "fixture-embedding", 1536
	cfg.ContextCompactionMode, cfg.MemoryAdaptiveExtractionMode = "off", "off"
	cfg.RerankerMode = "heuristic"
	cfg.ModelRequestCaptureMode, cfg.ModelRetryMaxAttempts = "metadata_only", 1
	cfg.ModelRequestsPerMinute, cfg.ModelTokensPerMinute, cfg.RunMaxModelCalls = 0, 0, 3
	application, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		fixture.release(httptest.NewRecorder(), nil)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := application.Close(ctx); err != nil {
			t.Error(err)
		}
	}()
	server := httptest.NewServer(application.server.Handler)
	defer server.Close()
	defer fixture.release(httptest.NewRecorder(), nil)
	client := &http.Client{Timeout: 20 * time.Second}
	call := func(method, path string, body any, status int, result any) {
		t.Helper()
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		request, err := http.NewRequestWithContext(t.Context(), method, server.URL+path, bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Content-Type", "application/json")
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != status {
			payload, _ := io.ReadAll(response.Body)
			t.Fatalf("%s %s: %d %s", method, path, response.StatusCode, payload)
		}
		if status == http.StatusTooManyRequests && response.Header.Get("Retry-After") == "" {
			t.Fatal("capacity rejection omitted Retry-After")
		}
		if result != nil && json.NewDecoder(response.Body).Decode(result) != nil {
			t.Fatal("invalid response JSON")
		}
	}
	response, err := client.Post(server.URL+"/api/chat", "application/json", strings.NewReader(`{"message":"stream-gate: original task"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	scanner := bufio.NewScanner(response.Body)
	observed := false
	for scanner.Scan() {
		if strings.Contains(scanner.Text(), "First token") {
			observed = true
			break
		}
	}
	if !observed {
		t.Fatalf("stream did not reach gate: %v", scanner.Err())
	}
	runs, err := application.store.ListRuns()
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs: %+v %v", runs, err)
	}
	run := runs[0]
	path := "/api/conversations/" + run.ConversationID + "/inputs"
	call("POST", path, map[string]string{"unexpected": "field"}, 400, nil)
	call("POST", path, map[string]string{"kind": "unknown", "run_id": run.ID, "content": "bad", "idempotency_key": "bad"}, 400, nil)
	input := domain.RunInputRequest{Kind: "steer", RunID: run.ID, Content: "STEERING_CANARY: use concise text", IdempotencyKey: "steer"}
	var steer, duplicate, followup, extra domain.RunInput
	call("POST", path, input, 202, &steer)
	call("POST", path, input, 202, &duplicate)
	if steer.ID != duplicate.ID {
		t.Fatal("duplicate receipt")
	}
	input.Content = "changed"
	call("POST", path, input, 409, nil)
	input.Kind, input.Content, input.IdempotencyKey = "follow_up", "A fresh independent task", "follow-up"
	call("POST", path, input, 202, &followup)
	input.IdempotencyKey = "withdraw"
	call("POST", path, input, 202, &extra)
	call("DELETE", path+"/"+extra.ID, nil, 200, &extra)
	call("DELETE", path+"/"+extra.ID, nil, 200, nil)
	// The real HTTP boundary must preserve accepted work when the queue fills.
	var filled []string
	for index := 0; index < 14; index++ {
		input.IdempotencyKey = "capacity-" + strings.Repeat("x", index+1)
		call("POST", path, input, 202, &extra)
		filled = append(filled, extra.ID)
	}
	input.IdempotencyKey = "capacity-overflow"
	call("POST", path, input, 429, nil)
	for _, id := range filled {
		call("DELETE", path+"/"+id, nil, 200, nil)
	}
	call("POST", path+"/start", nil, 409, nil)
	call("GET", "/api/conversations/missing/inputs", nil, 404, nil)
	fixture.release(httptest.NewRecorder(), nil)
	for scanner.Scan() {
	}
	if scanner.Err() != nil {
		t.Fatal(scanner.Err())
	}
	// Background dispatch can outlive the original HTTP response. Wait for its
	// persisted completion condition, never an arbitrary scheduling sleep.
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	var receipts []domain.RunInput
	for {
		call("GET", path, nil, 200, &receipts)
		ready := false
		for _, item := range receipts {
			if item.ID == followup.ID && item.Status == "applied" {
				var next domain.Run
				call("GET", "/api/runs/"+item.AppliedRunID, nil, 200, &next)
				ready = next.Status == domain.RunCompleted && next.ID != run.ID
			}
		}
		if ready {
			break
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatalf("follow-up did not finish: %+v", receipts)
		}
	}
	call("DELETE", path+"/"+steer.ID, nil, 409, nil)
	var messages []domain.Message
	call("GET", "/api/conversations/"+run.ConversationID+"/messages", nil, 200, &messages)
	for _, id := range []string{steer.ID, followup.ID} {
		count := 0
		for _, message := range messages {
			if message.ID == id {
				count++
			}
		}
		if count != 1 {
			t.Fatalf("input %s persisted %d messages", id, count)
		}
	}
}
