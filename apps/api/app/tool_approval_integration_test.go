package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/approval"
	"agentflow-platform/apps/api/internal/checkpoint"
	"agentflow-platform/apps/api/internal/clarification"
	"agentflow-platform/apps/api/internal/config"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/identity"
	"agentflow-platform/apps/api/internal/testsupport/pgfixture"
	"agentflow-platform/apps/api/internal/tool"
	"agentflow-platform/apps/api/internal/tool/filesystem"
	"agentflow-platform/apps/api/internal/tool/policy"
)

// Failure inventory: pending must not patch; approval is exact and single-use;
// rejection/expiry/cancel/revocation/drift/mixed batches cannot execute. Restart must retain
// original Tool-call and reasoning protocol, budget, scope and file receipts.
func TestProductionDurableToolApproval(t *testing.T) {
	for _, mode := range []string{"single", "multi_agent", "autonomous"} {
		for _, scenario := range []string{"happy", "undo", "lazy", "restart", "legacy_policy_restart", "claim_restart", "consume_restart", "write_restart", "completed_restart", "uncertain_restart", "reject", "cancel", "budget", "expired", "revoke", "policy_changed", "live_approval_policy_drift", "grant_revoked", "stale_args", "snapshot_drift", "root_drift", "prewrite_conflict", "corrupt_checkpoint", "corrupt_protocol", "mixed_batch", "legacy_mixed_batch", "concurrent", "generic_calculator", "generic_file_write", "generic_file_read", "generic_update_task_state", "generic_request_user_input", "generic_mcp_docs__read"} {
			if mode != "single" && !strings.HasPrefix(scenario, "generic_") && scenario != "happy" && scenario != "restart" && scenario != "claim_restart" && scenario != "write_restart" && scenario != "completed_restart" && scenario != "reject" && scenario != "budget" {
				continue
			}
			t.Run(mode+"/"+scenario, func(t *testing.T) { testProductionToolApproval(t, mode, scenario) })
		}
	}
}

func testProductionToolApproval(t *testing.T, mode, scenario string) {
	root := t.TempDir()
	t.Chdir(root)
	t.Setenv("AGENTFLOW_FILESYSTEM_WRITE_TEST", "1")
	t.Setenv("BROWSER_FIXTURE_KEY", "fixture-only")
	t.Setenv("EMBEDDING_API_KEY", "fixture-only")
	provider := newBrowserProvider()
	var changedPolicy sync.Once
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if scenario == "live_approval_policy_drift" && r.URL.Path == "/chat/completions" {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
			var input browserModelRequest
			if json.Unmarshal(body, &input) == nil {
				for _, message := range input.Messages {
					for _, call := range message.ToolCalls {
						if call.Function.Name == "file_write" {
							changedPolicy.Do(func() {
								path := filepath.Join(root, "tools.json")
								config, err := tool.LoadConfig(path)
								if err != nil {
									t.Error(err)
									return
								}
								for i := range config.SecurityPolicy.Rules {
									if config.SecurityPolicy.Rules[i].Tool == "file_patch" {
										config.SecurityPolicy.Rules[i].Action = policy.ActionApprovalRequired
									}
								}
								if err = tool.SaveConfig(path, config); err != nil {
									t.Error(err)
								}
							})
						}
					}
				}
			}
		}
		provider.respond(w, r)
	}))
	defer model.Close()
	route := filepath.Join(root, "routes.json")
	routes, _ := json.Marshal(map[string]any{"routes": []any{map[string]any{
		"id": "approval-fixture", "model": "fixture-model", "base_url": model.URL,
		"credential_environment": "BROWSER_FIXTURE_KEY", "request_timeout_seconds": 10,
		"context_window_tokens": 128000, "max_output_tokens": 8192,
		"priority": 100, "pricing": map[string]string{"source": "fixture"},
		"capabilities": map[string]any{"tool_calling": true, "streaming": true, "structured_output": true},
	}}})
	if err := os.WriteFile(route, routes, 0600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Load()
	cfg.DatabaseURL = pgfixture.DatabaseURL(t)
	cfg.AuthMode, cfg.TrustedSkillDirectories, cfg.SkillReviewManifestPath = "local", "", ""
	cfg.ModelRouteConfigPath, cfg.ToolConfigPath = route, filepath.Join(root, "tools.json")
	cfg.MemoryAdaptiveExtractionMode, cfg.ContextCompactionMode = "off", "off"
	cfg.EmbeddingBaseURL, cfg.EmbeddingModel, cfg.EmbeddingDimensions = model.URL, "fixture-embedding", 1536
	cfg.RouterMode, cfg.AutonomousMaxIterations, cfg.ToolSchemaMode = "query_match", 1, "eager"
	cfg.ModelRetryMaxAttempts, cfg.RunMaxModelCalls = 1, 25
	cfg.ModelRequestsPerMinute, cfg.ModelTokensPerMinute = 0, 0
	cfg.SandboxEnabled = false
	if scenario == "budget" {
		cfg.RunMaxToolCalls = 1
	}
	if scenario == "lazy" {
		cfg.ToolSchemaMode = "lazy"
	}
	browserFilesystemConfig(t, &cfg, root)
	var mcpControls http.Handler
	if scenario == "generic_mcp_docs__read" {
		mcpControls = browserMCPConfig(t, &cfg, root)
	}
	tools, err := tool.LoadConfig(cfg.ToolConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	for i := range tools.SecurityPolicy.Rules {
		if !strings.HasPrefix(scenario, "generic_") && tools.SecurityPolicy.Rules[i].Tool == "file_patch" && scenario != "live_approval_policy_drift" {
			tools.SecurityPolicy.Rules[i].Action = policy.ActionApprovalRequired
			if strings.HasPrefix(scenario, "legacy_") {
				tools.SecurityPolicy.Rules[i].Action = "ask"
			}
		}
	}
	if strings.HasPrefix(scenario, "generic_") {
		name := configuredApprovalTool(scenario)
		found := false
		for i := range tools.SecurityPolicy.Rules {
			if tools.SecurityPolicy.Rules[i].Tool == name {
				tools.SecurityPolicy.Rules[i].Action = policy.ActionApprovalRequired
				found = true
			}
		}
		if !found {
			var binding tool.Binding
			switch name {
			case "calculator":
				binding = tool.CalculatorTool()
			case clarification.ToolName:
				binding = clarification.Binding()
			default:
				t.Fatalf("missing real test Binding: %s", name)
			}
			tools.SecurityPolicy.Rules = append(tools.SecurityPolicy.Rules, policy.Rule{ID: "configured-approval", Tool: name, Action: policy.ActionApprovalRequired, Capability: binding.Descriptor.Security})
		}
	}
	if err = tool.SaveConfig(cfg.ToolConfigPath, tools); err != nil {
		t.Fatal(err)
	}
	application, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := application.Close(ctx); err != nil {
			t.Error(err)
		}
	}()
	server := httptest.NewServer(application.server.Handler)
	defer func() { server.Close() }()
	call := func(method, path string, input any, want int, out any) string {
		t.Helper()
		data, _ := json.Marshal(input)
		req, _ := http.NewRequestWithContext(t.Context(), method, server.URL+path, bytes.NewReader(data))
		req.Header.Set("Content-Type", "application/json")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		body, _ := io.ReadAll(res.Body)
		if res.StatusCode != want {
			t.Fatalf("%s %s: %d %s", method, path, res.StatusCode, body)
		}
		if out != nil {
			if err := json.Unmarshal(body, out); err != nil {
				t.Fatalf("decode: %v %s", err, body)
			}
		}
		return string(body)
	}
	var agent domain.Agent
	boundTools := []string{"file_write", "file_patch"}
	task := "tool-approval-gate: revise the managed report"
	if strings.HasPrefix(scenario, "generic_") {
		task = "configured-approval-gate:" + configuredApprovalTool(scenario)
		boundTools = []string{"calculator"}
		if name := configuredApprovalTool(scenario); name != "calculator" && name != "update_task_state" && name != clarification.ToolName {
			boundTools = append(boundTools, name)
		}
	}
	if scenario == "prewrite_conflict" {
		boundTools = append(boundTools, "file_read")
		task += " prewrite-correction"
	}
	if strings.HasSuffix(scenario, "mixed_batch") {
		task += " mixed-approval-batch"
	}
	call("POST", "/api/agents", map[string]any{"name": "Approval report editor", "system_prompt": "Edit the report using authorized tools.", "tools": boundTools, "routing_hints": map[string]any{"capabilities": []string{"report"}, "task_examples": []string{"tool-approval-gate: revise the managed report"}}}, 201, &agent)
	body := call("POST", "/api/chat", map[string]any{"agent_id": agent.ID, "mode": mode, "message": task}, 200, nil)
	var runID string
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "data: ") {
			var chunk domain.ChatChunk
			if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &chunk) == nil && chunk.RunID != "" {
				runID = chunk.RunID
			}
		}
	}
	if runID == "" {
		t.Fatalf("missing Run identity: %s", body)
	}
	if mode == "multi_agent" {
		var pending []domain.ToolApproval
		call("GET", "/api/runs/"+runID+"/tool-approvals", nil, 200, &pending)
		if len(pending) == 0 {
			body = call("POST", "/api/runs/"+runID+"/continue", map[string]any{"plan": "Edit the report after explicit approval."}, 200, nil)
		}
	}
	var approvals []map[string]any
	call("GET", "/api/runs/"+runID+"/tool-approvals", nil, 200, &approvals)
	if strings.HasPrefix(scenario, "generic_") {
		if mode == "multi_agent" && configuredApprovalTool(scenario) == "update_task_state" {
			// Approval never overrides the isolated Worker's existing prohibition
			// on writing parent Task State.
			var replay domain.RunReplay
			call("GET", "/api/runs/"+runID+"/replay", nil, 200, &replay)
			if len(approvals) != 0 || len(replay.TaskStateRevisions) != 0 {
				t.Fatal("approval expanded isolated Worker authority")
			}
			return
		}
		if mcpControls != nil {
			assertApprovalMCPCalls(t, mcpControls, 0)
		}
		configuredApprovalRun{call: call, root: root, runID: runID, tool: configuredApprovalTool(scenario)}.verify(t)
		if mcpControls != nil {
			assertApprovalMCPCalls(t, mcpControls, 1)
		}
		return
	}
	if strings.HasSuffix(scenario, "mixed_batch") {
		data, err := os.ReadFile(filepath.Join(root, "output", "approval-report.md"))
		if err != nil || string(data) != "Original report.\n" || len(approvals) != 0 || !strings.Contains(body, "tool_approval_batch_unsupported") {
			t.Fatalf("mixed batch was not rejected before effects: %s approvals=%#v err=%v stream=%s", data, approvals, err, body)
		}
		if _, err = os.Stat(filepath.Join(root, "output", "unreviewed.md")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("mixed batch executed another write: %v", err)
		}
		return
	}
	if scenario == "live_approval_policy_drift" {
		data, err := os.ReadFile(filepath.Join(root, "output", "approval-report.md"))
		if err != nil || string(data) != "Original report.\n" || len(approvals) != 0 {
			t.Fatalf("live approval_required bypassed frozen allow: %s approvals=%#v err=%v", data, approvals, err)
		}
		return
	}
	if len(approvals) != 1 || approvals[0]["status"] != "pending" {
		t.Fatalf("not durably pending: %s %#v", body, approvals)
	}
	contents, err := os.ReadFile(filepath.Join(root, "output", "approval-report.md"))
	if err != nil || string(contents) != "Original report.\n" {
		t.Fatalf("pending changed disk: %s %v", contents, err)
	}
	path := "/api/runs/" + runID + "/tool-approvals/" + approvals[0]["id"].(string)
	// Ordinary plan approval and user text cannot authorize this pending action.
	call("POST", "/api/runs/"+runID+"/continue", map[string]any{"plan": "I approve all tools"}, 409, nil)
	call("POST", "/api/runs/"+runID+"/resume", map[string]any{"user_input": "yes, execute everything"}, 409, nil)
	checkOriginal := func() {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(root, "output", "approval-report.md"))
		if err != nil || string(data) != "Original report.\n" {
			t.Fatalf("rejected approval modified disk: %s %v", data, err)
		}
	}
	db, err := sql.Open("pgx", cfg.DatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mutate := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(t.Context(), query, args...); err != nil {
			t.Fatal(err)
		}
	}
	if scenario == "reject" {
		call("POST", path+"/decision", map[string]any{"version": approvals[0]["version"], "decision": "reject", "idempotency_key": "reject-once"}, 200, nil)
		call("POST", path+"/resume", nil, 409, nil)
		checkOriginal()
		return
	}
	if scenario == "cancel" {
		call("POST", "/api/runs/"+runID+"/cancel", nil, 200, nil)
		call("POST", path+"/decision", map[string]any{"version": approvals[0]["version"], "decision": "approve", "idempotency_key": "too-late"}, 409, nil)
		checkOriginal()
		return
	}
	if scenario == "expired" {
		expired, _ := json.Marshal(time.Now().Add(-time.Hour))
		mutate(`UPDATE tool_approvals SET record=jsonb_set(record,'{expires_at}',$2::jsonb) WHERE id=$1`, approvals[0]["id"], expired)
		call("POST", path+"/decision", map[string]any{"version": approvals[0]["version"], "decision": "approve", "idempotency_key": "expired"}, 409, nil)
		call("GET", "/api/runs/"+runID+"/tool-approvals", nil, 200, &approvals)
		if approvals[0]["status"] != "expired" {
			t.Fatal(approvals)
		}
		checkOriginal()
		return
	}
	decision := map[string]any{"version": approvals[0]["version"], "decision": "approve", "idempotency_key": "review-one"}
	call("POST", path+"/decision", map[string]any{"version": 2, "decision": "approve", "idempotency_key": "stale"}, 409, nil)
	call("POST", path+"/decision", decision, 200, nil)
	call("POST", path+"/decision", decision, 200, nil)
	call("POST", path+"/decision", map[string]any{"version": 1, "decision": "reject", "idempotency_key": "conflict"}, 409, nil)
	switch scenario {
	case "prewrite_conflict":
		if err = os.WriteFile(filepath.Join(root, "output", "approval-report.md"), []byte("Original report changed.\n"), 0600); err != nil {
			t.Fatal(err)
		}
	case "snapshot_drift":
		mutate(`UPDATE runs SET runtime_snapshot=jsonb_set(runtime_snapshot,'{agent,name}','"changed Snapshot"') WHERE id=$1`, runID)
		call("POST", path+"/resume", nil, 409, nil)
		checkOriginal()
		return
	case "root_drift":
		if err = os.WriteFile(cfg.FilesystemRootsPath, []byte(`{"roots":[]}`), 0600); err != nil {
			t.Fatal(err)
		}
		call("POST", path+"/resume", nil, 409, nil)
		checkOriginal()
		return
	case "revoke":
		call("POST", path+"/decision", map[string]any{"version": 1, "decision": "revoke", "idempotency_key": "revoke"}, 200, nil)
		call("POST", path+"/resume", nil, 409, nil)
		checkOriginal()
		return
	case "stale_args":
		mutate(`UPDATE tool_approvals SET record=jsonb_set(record,'{arguments,new_text}','"Unreviewed edit"') WHERE id=$1`, approvals[0]["id"])
		call("POST", path+"/resume", nil, 409, nil)
		checkOriginal()
		return
	case "policy_changed":
		for i := range tools.SecurityPolicy.Rules {
			if tools.SecurityPolicy.Rules[i].Tool == "file_patch" {
				tools.SecurityPolicy.Rules[i].Action = policy.ActionDeny
			}
		}
		if err = tool.SaveConfig(cfg.ToolConfigPath, tools); err != nil {
			t.Fatal(err)
		}
		call("POST", path+"/resume", nil, 409, nil)
		checkOriginal()
		return
	case "grant_revoked":
		mutate(`UPDATE workspace_tool_config SET allowed_tools=allowed_tools-'file_patch',revision=revision+1`)
		call("POST", path+"/resume", nil, 409, nil)
		checkOriginal()
		return
	case "corrupt_checkpoint":
		mutate(`UPDATE tool_approvals SET checkpoint='{}' WHERE id=$1`, approvals[0]["id"])
		call("POST", path+"/resume", nil, 500, nil)
		checkOriginal()
		return
	case "corrupt_protocol":
		mutate(`UPDATE tool_approvals SET checkpoint=jsonb_set(checkpoint,ARRAY['Continuation','Messages',((jsonb_array_length(checkpoint->'Continuation'->'Messages'))-1)::text,'tool_calls','0','function','arguments'],to_jsonb('{"new_text":"Unreviewed edit"}'::text)) WHERE id=$1`, approvals[0]["id"])
		// Corrupt server-owned protocol is an internal failure after claiming;
		// unlike stale client metadata, it must also settle the admitted Run.
		call("POST", path+"/resume", nil, 500, nil)
		checkOriginal()
		return
	case "claim_restart", "consume_restart", "write_restart", "completed_restart", "uncertain_restart":
		target := application.store.(*observableStore).Store.(approval.Store)
		claimed, claimErr := target.ClaimToolApproval(t.Context(), identity.SuperUserID, approvals[0]["workspace_id"].(string), runID, approvals[0]["id"].(string))
		if claimErr != nil {
			t.Fatal(claimErr)
		}
		if scenario == "consume_restart" {
			if _, err = target.ConsumeToolApproval(t.Context(), identity.SuperUserID, claimed.WorkspaceID, claimed.ID, claimed.Claim, approval.BindingHash(claimed.ToolApproval)); err != nil {
				t.Fatal(err)
			}
		}
		if scenario != "claim_restart" && scenario != "consume_restart" {
			// Seed only the exact crash window using the real Binding, journal,
			// approval consume and events; subsequent recovery is production code.
			fs, err := filesystem.New(cfg.FilesystemRootsPath, application.store.(*observableStore).Store)
			if err != nil {
				t.Fatal(err)
			}
			manager, err := tool.NewManager(cfg.ToolConfigPath, fs.Bindings()...)
			if err != nil {
				t.Fatal(err)
			}
			catalog, err := manager.Catalog()
			if err != nil {
				t.Fatal(err)
			}
			if _, err = application.store.CreateRunEvent(domain.RunEvent{RunID: runID, ConversationID: claimed.ConversationID, StageID: claimed.StageID, TurnID: claimed.TurnID, Type: domain.EventTurnStarted, Payload: map[string]any{"agent_id": agent.ID}}); err != nil {
				t.Fatal(err)
			}
			var journal tool.ToolEffectJournal = application.store
			if scenario == "uncertain_restart" {
				journal = approvalSettlementFault{journal}
			}
			ctx := event.WithScope(t.Context(), event.Scope{RunID: runID, ConversationID: claimed.ConversationID, StageID: claimed.StageID, TurnID: claimed.TurnID})
			result := tool.NewExecutor(catalog, tool.ExecutorOptions{EffectJournal: journal, Tracer: event.NewToolExecutionTracer(event.NewRecorder(application.store), runID, claimed.StageID), ConsumeApproval: func(ctx context.Context, call tool.ExecutionRequest) error {
				_, err := target.ConsumeToolApproval(ctx, identity.SuperUserID, claimed.WorkspaceID, claimed.ID, claimed.Claim, approval.BindingHash(claimed.ToolApproval))
				return err
			}}).Execute(ctx, tool.ExecutionRequest{ApprovalID: claimed.ID, RunID: runID, StageID: claimed.StageID, TurnID: claimed.TurnID, CallID: claimed.ToolCallID, Tool: claimed.Tool, Arguments: claimed.Arguments})
			if scenario == "uncertain_restart" {
				if result.Error == nil || result.Error.Code != tool.ErrorEffectJournal {
					t.Fatal(result)
				}
			} else if result.Error != nil {
				t.Fatal(result.Error)
			}
			if scenario == "completed_restart" {
				if _, err = application.store.CreateRunEvent(domain.RunEvent{RunID: runID, ConversationID: claimed.ConversationID, StageID: claimed.StageID, TurnID: claimed.TurnID, Type: domain.EventTurnCompleted, Payload: map[string]any{"stop_reason": "completed", "output": "Report edit verified."}}); err != nil {
					t.Fatal(err)
				}
				if claimed.StageID != "" {
					step, err := application.store.UpdateCollaborationStep(claimed.StageID, domain.CollaborationStepCompleted, "Report edit verified.", "")
					if err != nil {
						t.Fatal(err)
					}
					if _, err = checkpoint.NewInternalProvider(application.store).RecordStageTransition(ctx, step, domain.EventStageCompleted); err != nil {
						t.Fatal(err)
					}
				}
			}
		}
		mutate(`UPDATE runs SET heartbeat_at=NOW()-INTERVAL '1 hour' WHERE id=$1`, runID)
	}
	if strings.HasSuffix(scenario, "restart") {
		server.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err = application.Close(ctx)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		application, err = New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		server = httptest.NewServer(application.server.Handler)
	}
	if scenario == "uncertain_restart" {
		call("POST", path+"/resume", nil, 409, nil)
		contents, err := os.ReadFile(filepath.Join(root, "output", "approval-report.md"))
		if err != nil || string(contents) != "Approved report.\n" {
			t.Fatalf("uncertain publication %s %v", contents, err)
		}
		return
	}
	var resumed string
	if scenario == "concurrent" {
		type response struct {
			status int
			body   string
			err    error
		}
		responses := make(chan response, 2)
		for range 2 {
			go func() {
				req, _ := http.NewRequestWithContext(t.Context(), "POST", server.URL+path+"/resume", nil)
				res, err := http.DefaultClient.Do(req)
				if err != nil {
					responses <- response{err: err}
					return
				}
				defer res.Body.Close()
				data, _ := io.ReadAll(res.Body)
				responses <- response{status: res.StatusCode, body: string(data)}
			}()
		}
		accepted, rejected := 0, 0
		for range 2 {
			res := <-responses
			if res.err != nil {
				t.Fatal(res.err)
			}
			if res.status == 200 {
				accepted++
				resumed = res.body
			} else if res.status == 409 {
				rejected++
			} else {
				t.Fatalf("concurrent status=%d %s", res.status, res.body)
			}
		}
		if accepted != 1 || rejected != 1 {
			t.Fatalf("claims: accepted=%d rejected=%d", accepted, rejected)
		}
	} else {
		resumed = call("POST", path+"/resume", nil, 200, nil)
	}
	if scenario == "budget" {
		if !strings.Contains(resumed, "budget") {
			t.Fatal(resumed)
		}
		checkOriginal()
		return
	}
	if scenario == "prewrite_conflict" {
		call("GET", "/api/runs/"+runID+"/tool-approvals", nil, 200, &approvals)
		if len(approvals) != 2 || approvals[1]["status"] != "pending" || approvals[0]["arguments_hash"] == approvals[1]["arguments_hash"] {
			t.Fatalf("corrected call reused approval: %s %#v", resumed, approvals)
		}
		path = "/api/runs/" + runID + "/tool-approvals/" + approvals[1]["id"].(string)
		call("POST", path+"/decision", map[string]any{"version": 1, "decision": "approve", "idempotency_key": "corrected-call"}, 200, nil)
		resumed = call("POST", path+"/resume", nil, 200, nil)
	}
	var answer strings.Builder
	for _, line := range strings.Split(resumed, "\n") {
		if strings.HasPrefix(line, "data: ") {
			var item domain.RunEvent
			if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &item) == nil && item.Type == domain.EventModelDelta {
				if reset, _ := item.Payload["reset"].(bool); reset {
					answer.Reset()
				}
				delta, _ := item.Payload["delta"].(string)
				answer.WriteString(delta)
			}
		}
	}
	if answer.String() != "Report edit verified." {
		t.Fatalf("continuation failed: %s", resumed)
	}
	contents, err = os.ReadFile(filepath.Join(root, "output", "approval-report.md"))
	wantContent := "Approved report.\n"
	if scenario == "prewrite_conflict" {
		wantContent = "Approved report changed.\n"
	}
	if err != nil || string(contents) != wantContent {
		t.Fatalf("approved content: %s %v", contents, err)
	}
	call("POST", path+"/resume", nil, 409, nil)
	var replay map[string]any
	call("GET", "/api/runs/"+runID+"/replay", nil, 200, &replay)
	run, _, err := application.store.GetRun(runID)
	if err != nil || run.Status != domain.RunCompleted {
		t.Fatalf("not completed: %+v %v", run, err)
	}
	events, err := application.store.ListRunEvents(runID)
	if err != nil {
		t.Fatal(err)
	}
	if failures := event.CheckRuntimeInvariants(run, events); len(failures) > 0 {
		t.Fatal(failures)
	}
	effects, err := application.store.ListToolEffects(runID)
	if err != nil {
		t.Fatal(err)
	}
	patches, committed := 0, 0
	for _, effect := range effects {
		if effect.ToolName == "file_patch" {
			patches++
			if effect.Status == domain.ToolEffectCommitted {
				committed++
			} else if scenario != "prewrite_conflict" || effect.Status != domain.ToolEffectFailed {
				t.Fatal(effect)
			}
		}
	}
	wantPatches := 1
	if scenario == "prewrite_conflict" {
		wantPatches = 2
	}
	if patches != wantPatches || committed != 1 {
		t.Fatalf("patch receipts=%d", patches)
	}
	if scenario == "undo" {
		for _, effect := range effects {
			if effect.ToolName == "file_patch" {
				base := "/api/runs/" + runID + "/tool-effects/" + effect.IdempotencyKey
				var preview tool.CompensationPreview
				call("GET", base+"/compensation-preview", nil, 200, &preview)
				if !preview.Available {
					t.Fatal(preview)
				}
				call("POST", base+"/reconcile", map[string]any{"command_id": "human-undo", "action": "compensate", "expected_version": effect.Version, "actor": "current-owner", "reason": "Restore the report"}, 200, nil)
				checkOriginal()
			}
		}
		call("GET", "/api/runs/"+runID+"/tool-approvals", nil, 200, &approvals)
		if len(approvals) != 1 {
			t.Fatal("human Undo created another Tool approval")
		}
	}
	evidence, _ := json.MarshalIndent(map[string]any{"schema": "tool-approval-evidence-v1", "run_id": runID, "input": "tool-approval-gate", "approvals": approvals, "replay": replay, "checks": []string{"pending has zero patch", "duplicate decision", "exact patch", "duplicate resume rejected"}, "limitations": []string{"deterministic provider", "managed-output file_patch only"}}, "", "  ")
	if destination := os.Getenv("TOOL_APPROVAL_EVIDENCE_PATH"); destination != "" {
		if err := os.WriteFile(destination, evidence, 0600); err != nil {
			t.Fatal(err)
		}
	}
}

type approvalSettlementFault struct{ tool.ToolEffectJournal }

func (approvalSettlementFault) CompleteToolEffect(string, []byte) (domain.ToolEffectRecord, error) {
	return domain.ToolEffectRecord{}, errors.New("fixture final settlement unavailable")
}
