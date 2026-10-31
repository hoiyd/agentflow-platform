package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"agentflow-platform/apps/api/app/runcommands"
	"agentflow-platform/apps/api/internal/concurrency"
	"agentflow-platform/apps/api/internal/config"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/failure"
	"agentflow-platform/apps/api/internal/identity"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/testsupport/pgfixture"
)

// Real HTTP/commands/Runtime/Postgres; only provider transport is deterministic.
// Legacy fixtures preserve input.run_id even when a newer ordinary Run exists.
func TestProductionInboxPlanningIntentBoundary(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	fixture := newBrowserProvider()
	provider := httptest.NewServer(http.HandlerFunc(fixture.respond))
	defer provider.Close()
	t.Setenv("BROWSER_FIXTURE_KEY", "fixture-only")
	t.Setenv("EMBEDDING_API_KEY", "fixture-only")
	t.Setenv("TAVILY_API_KEY", "")
	cfg := config.Load()
	cfg.DatabaseURL = pgfixture.DatabaseURL(t)
	cfg.ModelRouteConfigPath, cfg.ToolConfigPath = filepath.Join(root, "routes.json"), filepath.Join(root, "tools.json")
	writeHeadlessJSON(t, cfg.ModelRouteConfigPath, map[string]any{"routes": []any{map[string]any{"id": "planning-inbox", "model": "fixture-model", "base_url": provider.URL, "credential_environment": "BROWSER_FIXTURE_KEY", "request_timeout_seconds": 20, "context_window_tokens": 128000, "max_output_tokens": 8192, "priority": 100, "capabilities": map[string]bool{"tool_calling": true, "streaming": true, "structured_output": true}, "pricing": map[string]string{"source": "fixture"}}}})
	cfg.AuthMode, cfg.SandboxEnabled = "local", false
	cfg.TrustedSkillDirectories, cfg.FilesystemRootsPath, cfg.ProjectGuidancePath, cfg.MCPConfigPath = root, "", "", ""
	cfg.EmbeddingBaseURL, cfg.EmbeddingModel, cfg.EmbeddingDimensions = provider.URL, "fixture-embedding", 1536
	cfg.ContextCompactionMode, cfg.MemoryAdaptiveExtractionMode, cfg.RerankerMode = "off", "off", "heuristic"
	cfg.ModelRequestsPerMinute, cfg.ModelTokensPerMinute, cfg.ModelRetryMaxAttempts = 0, 0, 1
	a, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := a.Close(context.Background()); err != nil {
			t.Error(err)
		}
	}()
	s, err := store.NewPostgresStore(cfg.DatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	db, err := sql.Open("pgx", cfg.DatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	workspace, err := s.DefaultWorkspace(t.Context(), identity.SuperUserID)
	if err != nil {
		t.Fatal(err)
	}
	agent, err := s.ForWorkspace(domain.NewWorkspaceScope(workspace)).CreateAgent(domain.Agent{Name: "Planning inbox", Executor: "llm", Tools: []string{}, Skills: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	call := func(t *testing.T, path string, body any, status int) []byte {
		t.Helper()
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(data))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Workspace-ID", workspace)
		res := httptest.NewRecorder()
		a.ServeHTTP(res, req)
		if res.Code != status || status == 200 && strings.Contains(res.Body.String(), "event: error") {
			t.Fatalf("%s: %d %s", path, res.Code, res.Body.String())
		}
		return res.Body.Bytes()
	}
	for _, scenario := range []string{"enqueue", "planning-latest", "ordinary-latest"} {
		t.Run(scenario, func(t *testing.T) {
			newer := scenario == "ordinary-latest"
			conv, err := s.CreateConversationInWorkspace(workspace, "Planning follow-up")
			if err != nil {
				t.Fatal(err)
			}
			chat := domain.ChatRequest{ConversationID: conv.ID, AgentID: agent.ID, Mode: "single", Message: "planning-gate: propose only", ExecutionConstraint: domain.ExecutionPlanOnly}
			call(t, "/api/chat", chat, 200)
			runs, err := s.ListRuns()
			if err != nil {
				t.Fatal(err)
			}
			var source domain.Run
			for _, run := range runs {
				if run.ConversationID == conv.ID {
					source = run
					break
				}
			}
			if source.Status != domain.RunCompleted || source.RuntimeSnapshot == nil || source.RuntimeSnapshot.ExecutionConstraint != domain.ExecutionPlanOnly {
				t.Fatalf("planning source missing: %+v", source)
			}
			path := "/api/conversations/" + conv.ID + "/inputs"
			input := domain.RunInputRequest{Kind: "follow_up", RunID: source.ID, Mode: "single", AgentID: agent.ID, Content: "Execute implicitly", IdempotencyKey: "fresh"}
			if scenario == "enqueue" {
				body := call(t, path, input, 409)
				if !bytes.Contains(body, []byte("input_plan_only")) {
					t.Fatalf("non-explicit planning rejection: %s", body)
				}
				items, err := s.ListRunInputs(t.Context(), identity.SuperUserID, workspace, conv.ID)
				if err != nil || len(items) != 0 {
					t.Fatalf("rejection wrote receipt: %+v %v", items, err)
				}
				return
			}
			input.IdempotencyKey = "legacy"
			data, err := json.Marshal(input)
			if err != nil {
				t.Fatal(err)
			}
			id := store.NewID("input")
			if _, err := db.Exec(`INSERT INTO conversation_inputs(id,owner_user_id,workspace_id,conversation_id,run_id,request) VALUES($1,$2,$3,$4,$5,$6)`, id, identity.SuperUserID, workspace, conv.ID, source.ID, data); err != nil {
				t.Fatal(err)
			}
			before, err := s.ListMessages(conv.ID)
			if err != nil {
				t.Fatal(err)
			}
			if newer {
				// Successful ordinary completion invokes production automatic dispatch.
				chat.ExecutionConstraint, chat.Message = "", "A separately requested task"
				call(t, "/api/chat", chat, 200)
			}
			for attempt := 0; attempt < 2; attempt++ {
				if body := call(t, path+"/start", nil, 409); !bytes.Contains(body, []byte("input_plan_only")) {
					t.Errorf("legacy dispatch did not explain planning intent: %s", body)
				}
			}
			capacity := &planningInboxCapacity{}
			commands := runcommands.Commands{Store: s, Capacity: capacity}
			err = commands.Start(t.Context(), runcommands.Scope{Owner: identity.SuperUserID, Workspace: domain.NewWorkspaceScope(workspace)}, domain.ChatRequest{ConversationID: conv.ID, AgentID: agent.ID, Mode: "single", Message: input.Content}, nil, &runcommands.Followup{Input: domain.RunInput{ID: id, RunID: source.ID}, AllowStopped: true}, runcommands.Observer{})
			if err == nil || failure.Describe(err).Code != "input_plan_only" || capacity.called {
				t.Errorf("command bypassed pre-admission policy: %v capacity=%t", err, capacity.called)
			}
			items, err := s.ListRunInputs(t.Context(), identity.SuperUserID, workspace, conv.ID)
			if err != nil || len(items) != 1 || items[0].Status != "queued" || items[0].AppliedRunID != "" {
				t.Fatalf("legacy consumed: %+v %v", items, err)
			}
			after, err := s.ListMessages(conv.ID)
			expected := len(before)
			if newer {
				expected += 2
			}
			if err != nil || len(after) != expected {
				t.Errorf("implicit successor wrote messages: got=%d want=%d %v", len(after), expected, err)
			}
			var count int
			expected = 1
			if newer {
				expected = 2
			}
			if err := db.QueryRow(`SELECT count(*) FROM runs WHERE conversation_id=$1`, conv.ID).Scan(&count); err != nil || count != expected {
				t.Errorf("implicit successor wrote Run: count=%d %v", count, err)
			}
		})
	}
}

type planningInboxCapacity struct{ called bool }

func (c *planningInboxCapacity) Reserve() (*concurrency.Reservation, error) {
	c.called = true
	return nil, errors.New("planning follow-up reached admission")
}
