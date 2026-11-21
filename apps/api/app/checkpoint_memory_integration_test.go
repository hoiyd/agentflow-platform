package app

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/inference/routing"
	"agentflow-platform/apps/api/internal/testsupport/pgfixture"
)

func TestProductionCheckpointMemoryRevision(t *testing.T) {
	database := pgfixture.DatabaseURL(t)
	root := t.TempDir()
	t.Chdir(root)
	t.Setenv("TAVILY_API_KEY", "")
	fixture := newBrowserProvider()
	var mu sync.Mutex
	var requests []browserModelRequest
	modelServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/chat/completions") {
			body, err := io.ReadAll(r.Body)
			var input browserModelRequest
			if err != nil || json.Unmarshal(body, &input) != nil {
				http.Error(w, "invalid model request", http.StatusBadRequest)
				return
			}
			mu.Lock()
			requests = append(requests, input)
			mu.Unlock()
			r.Body = io.NopCloser(bytes.NewReader(body))
		}
		fixture.respond(w, r)
	}))
	defer modelServer.Close()
	cfg := durableTestConfig(t, root, database, routing.RouteConfig{ID: "checkpoint-memory-fixture", BaseURL: modelServer.URL, RequestTimeoutSeconds: 10})
	start := func() (*Application, *httptest.Server) {
		t.Helper()
		app, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		return app, httptest.NewServer(app)
	}
	app, server := start()
	stop := func() {
		server.Close()
		if err := closeDurableTestApplication(app); err != nil {
			t.Fatal(err)
		}
	}
	defer func() { stop() }()
	call := func(method, path string, input, out any, status int) string {
		t.Helper()
		return durableTestRequest(t, server, method, path, input, status, out)
	}
	const (
		task    = "clarification-gate: prepare a report, audience unspecified"
		keep    = "Use the release report template."
		old     = "The report window is January."
		removed = "Use the retired service roster."
		updated = "The report window is February."
	)
	memories := map[string]domain.Memory{}
	for _, content := range []string{keep, old, removed} {
		var item domain.Memory
		call("POST", "/api/memories", map[string]string{"kind": "fact", "content": content}, &item, 201)
		memories[content] = item
	}
	var agent domain.Agent
	call("POST", "/api/agents", map[string]any{"name": "Checkpoint Memory", "system_prompt": "Prepare the report.", "memory_enabled": true, "retrieval_enabled": false, "tools": []string{}}, &agent, 201)
	initial := call("POST", "/api/chat", domain.ChatRequest{AgentID: agent.ID, Mode: "single", Message: task}, nil, 200)
	if !strings.Contains(initial, "waiting_for_user") {
		t.Fatalf("not paused: %s", initial)
	}
	runs, err := app.store.ListRunsByWorkspace(agent.WorkspaceID)
	if err != nil || len(runs) != 1 || runs[0].Status != domain.RunWaitingForUser {
		t.Fatalf("no waiting Run: %+v, %v", runs, err)
	}
	runURL := "/api/runs/" + runs[0].ID
	var questions []domain.Clarification
	call("GET", runURL+"/clarifications", nil, &questions, 200)
	if len(questions) != 1 || len(questions[0].Questions) != 1 {
		t.Fatalf("missing clarification: %+v", questions)
	}
	q := questions[0]
	mu.Lock()
	var before string
	for _, req := range requests {
		for _, msg := range req.Messages {
			before += msg.Content + "\n"
		}
	}
	requests = nil
	mu.Unlock()
	for _, content := range []string{keep, old, removed} {
		if !strings.Contains(before, content) {
			t.Fatalf("initial request did not recall %q", content)
		}
	}
	// Use real revision writes, then restart so the follow-up uses the durable checkpoint.
	for _, mutation := range []struct{ original, action, content string }{
		{old, "replace", updated},
		{removed, "delete", ""},
	} {
		item := memories[mutation.original]
		call("POST", "/api/memories/"+item.ID+"/mutations", domain.MemoryMutation{
			OperationID: mutation.action + "-before-resume", ExpectedVersion: item.Version,
			Action: mutation.action, Content: mutation.content, Actor: "operator", Reason: "fixture revision",
		}, nil, 200)
	}
	stop()
	app, server = start()
	path := runURL + "/clarifications/" + q.ID
	call("POST", path+"/answers", domain.ClarificationAnswer{
		Version: q.Version, IdempotencyKey: "resume-memory-revision",
		Answers: map[string]string{q.Questions[0].ID: "Engineering leads"},
	}, nil, 200)
	resumed := call("POST", path+"/resume", nil, nil, 200)
	if done := durableTestDone(t, resumed); done["status"] != "completed" {
		t.Fatalf("resume did not complete: %s", resumed)
	}
	mu.Lock()
	after := append([]browserModelRequest(nil), requests...)
	mu.Unlock()
	found := false
	for _, req := range after {
		var prompt strings.Builder
		answers := false
		for _, msg := range req.Messages {
			prompt.WriteString(msg.Content)
			answers = answers || (msg.Role == "tool" && msg.ToolCallID == q.ToolCallID)
		}
		for _, content := range []string{old, removed, updated} {
			if strings.Contains(prompt.String(), content) {
				t.Errorf("request after Resume carried %q", content)
			}
		}
		if answers {
			found = true
			if !strings.Contains(prompt.String(), keep) {
				t.Errorf("unchanged Memory disappeared from the resumed request")
			}
		}
	}
	if !found {
		t.Fatal("no real provider request carried the durable clarification answer")
	}
	t.Logf("checkpoint memory revision: run=%s mode=single fixture=checkpoint-memory-fixture restart=true initial_memories=3 retained=1 replaced=1 deleted=1 resumed_requests=%d; deterministic provider", runs[0].ID, len(after))
}
