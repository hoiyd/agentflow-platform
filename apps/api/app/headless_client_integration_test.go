package app

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/config"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/identity"
	"agentflow-platform/apps/api/internal/runclient"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/testsupport/oidcfixture"
	"agentflow-platform/apps/api/internal/testsupport/pgfixture"
	"agentflow-platform/apps/api/internal/tool"
	"agentflow-platform/apps/api/internal/tool/filesystem"
	"agentflow-platform/apps/api/internal/tool/policy"
)

// CLI -> production composition -> real provider protocol/Bindings -> Postgres.
// Inventory: headless-run-client.md. No API success or Runtime is mocked here.
func TestHeadlessClientProductionRunLifecycle(t *testing.T) {
	database := pgfixture.DatabaseURL(t)
	moduleDir, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	binary := filepath.Join(root, "agentflow")
	build := exec.CommandContext(t.Context(), "go", "build", "-o", binary, "./cmd/agentflow")
	build.Dir = moduleDir
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v %s", err, output)
	}
	t.Chdir(root)
	provider := newBrowserProvider()
	remote := httptest.NewServer(http.HandlerFunc(provider.respond))
	t.Cleanup(remote.Close)
	idp := oidcfixture.New(t, "http://127.0.0.1:8080/api/auth/callback")
	t.Setenv("OIDC_CLIENT_SECRET", "fixture-only")
	t.Setenv("HEADLESS_FIXTURE_KEY", "fixture-only")
	t.Setenv("MCP_CREDENTIAL_ENCRYPTION_KEY", "")
	t.Setenv("TAVILY_API_KEY", "")
	t.Setenv("EMBEDDING_API_KEY", "fixture-only")
	cfg := config.Load()
	cfg.DatabaseURL, cfg.ToolConfigPath = database, filepath.Join(root, "tools.json")
	cfg.AuthMode, cfg.OIDCIssuer, cfg.OIDCClientID = "oidc", idp.Server.URL, "fixture-client"
	cfg.OIDCRedirectURL, cfg.AuthWebURL = "http://127.0.0.1:8080/api/auth/callback", "http://127.0.0.1:3000"
	cfg.AllowedOrigins = cfg.AuthWebURL
	cfg.ModelRouteConfigPath = filepath.Join(root, "routes.json")
	routes := map[string]any{"routes": []any{map[string]any{"id": "headless-fixture", "model": "fixture-model", "base_url": remote.URL, "credential_environment": "HEADLESS_FIXTURE_KEY", "request_timeout_seconds": 30, "context_window_tokens": 128000, "max_output_tokens": 8192, "priority": 100, "capabilities": map[string]any{"tool_calling": true, "streaming": true, "structured_output": true, "structured_output_format": "openai_json_schema"}}}}
	routes["routes"].([]any)[0].(map[string]any)["pricing"] = map[string]any{"source": "fixture"}
	writeHeadlessJSON(t, cfg.ModelRouteConfigPath, routes)
	cfg.EmbeddingBaseURL, cfg.EmbeddingModel, cfg.EmbeddingDimensions = remote.URL, "fixture-embedding", 1536
	cfg.MemoryAdaptiveExtractionMode, cfg.ContextCompactionMode = "off", "off"
	cfg.MCPConfigPath, cfg.MCPWorkspaceLocalOrigins, cfg.TrustedSkillDirectories, cfg.FilesystemRootsPath = "", "", "", ""
	cfg.SandboxEnabled, cfg.RouterMode, cfg.AutonomousMaxIterations = false, "query", 1
	cfg.ModelRequestsPerMinute, cfg.ModelTokensPerMinute, cfg.ModelRetryMaxAttempts = 0, 0, 1
	cfg.ToolSchemaMode, cfg.ModelRequestCaptureMode = "eager", "metadata_only"
	cfg.RunMaxModelCalls = 24
	cfg.MaxConcurrentRuns, cfg.RunQueueSize = 1, 0
	storage, err := store.NewPostgresStore(database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { storage.Close() })
	if err := storage.InitializeWorkspaceLifecycle(t.Context()); err != nil {
		t.Fatal(err)
	}
	spaces := pgfixture.GrantMemberships(t, database, idp.Server.URL, "headless-owner", "Headless reports")
	foreign := pgfixture.GrantMemberships(t, database, idp.Server.URL, "other-owner", "Foreign reports")[0]
	headlessFilesystemConfig(t, &cfg, root, spaces[0], storage)
	application, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := application.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	chatFinished := make(chan struct{}, 32)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		application.ServeHTTP(w, r)
		if r.URL.Path == "/api/chat" {
			chatFinished <- struct{}{}
		}
	}))
	t.Cleanup(server.Close)
	token := strings.Repeat("h", 43)
	hash := sha256.Sum256([]byte(token))
	if err := storage.CreateSession(t.Context(), hex.EncodeToString(hash[:]), identity.UserID(idp.Server.URL, "headless-owner"), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	session := filepath.Join(root, "session")
	if err := os.WriteFile(session, []byte("agentflow_session="+token), 0600); err != nil {
		t.Fatal(err)
	}
	base := []string{"--api-url", server.URL, "--web-url", cfg.AuthWebURL, "--workspace", spaces[0], "--session-file", session, "--deadline", "30s"}
	invoke := func(command string, extra ...string) (int, string, string) {
		var out, diagnostic bytes.Buffer
		args := append([]string{command}, base...)
		args = append(args, extra...)
		exit := runclient.Execute(t.Context(), args, &out, &diagnostic)
		if command == "start" && !strings.Contains(strings.Join(extra, " "), "--detach") {
			select {
			case <-chatFinished:
			case <-t.Context().Done():
				t.Fatal("chat request did not release its execution reservation")
			}
		}
		return exit, out.String(), diagnostic.String()
	}
	// Owned Agent creation uses the same endpoint and grants as the workbench.
	req, _ := http.NewRequest(http.MethodPost, server.URL+"/api/agents", strings.NewReader(`{"name":"Headless reporter","system_prompt":"Extract a bounded report from supplied material.","tools":["calculator","file_read","file_write","file_patch"],"skills":["fixture-method"],"memory_enabled":false,"retrieval_enabled":false}`))
	req.Header.Set("X-Workspace-ID", spaces[0])
	req.Header.Set("Origin", cfg.AuthWebURL)
	req.AddCookie(&http.Cookie{Name: "agentflow_session", Value: token})
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var agent domain.Agent
	if err := json.NewDecoder(response.Body).Decode(&agent); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 201 {
		t.Fatalf("create Agent: %d %+v", response.StatusCode, agent)
	}
	requestPath := filepath.Join(root, "request.json")
	fileRequest := map[string]any{"agent_id": agent.ID, "mode": "single", "message": "filesystem-write-gate-0: use the report skill, save and verify a report"}
	writeHeadlessJSON(t, requestPath, fileRequest)
	fileEvidence := filepath.Join(root, "file-report.json")
	fileArgs := append([]string{"start"}, base...)
	fileArgs = append(fileArgs, "--request", requestPath, "--output", fileEvidence)
	fileCommand := exec.CommandContext(t.Context(), binary, fileArgs...)
	if output, err := fileCommand.CombinedOutput(); err != nil {
		t.Fatalf("file report process: %v %s", err, output)
	}
	select {
	case <-chatFinished:
	case <-t.Context().Done():
		t.Fatal("file report handler did not finish")
	}
	if content, err := os.ReadFile(filepath.Join(root, "output", "report-0.md")); err != nil || string(content) != "Verified report evidence." {
		t.Fatalf("report file=%q err=%v", content, err)
	}
	schema := map[string]any{"type": "object", "additionalProperties": false, "required": []string{"summary", "facts"}, "properties": map[string]any{"summary": map[string]any{"type": "string"}, "facts": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}}}
	request := map[string]any{"agent_id": agent.ID, "mode": "single", "message": "structured-output-gate: Extract supplied material: Project Alpha uses Go. Total 2 + 3 deployments.", "completion_contract": map[string]any{"output": map[string]any{"mode": "strict", "verifier_id": "report"}, "verifiers": []any{map[string]any{"id": "report", "type": "json_schema", "required": true, "config": map[string]any{"schema": schema}}}, "policy": map[string]any{"max_attempts": 1, "on_exhausted": "fail"}}}
	writeHeadlessJSON(t, requestPath, request)
	successfulInput, _ := os.ReadFile(requestPath)
	contractInput := request["completion_contract"]
	reportPath := filepath.Join(root, "evidence.json")
	exit, out, diagnostic := invoke("start", "--request", requestPath, "--output", reportPath)
	if exit != 0 {
		t.Fatalf("start: exit=%d %s %s", exit, out, diagnostic)
	}
	data, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatal(err)
	}
	var report runclient.Report
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	if report.Replay.Run.Status != "completed" || report.Replay.Run.VerificationStatus != "passed" || report.Replay.StructuredResult == nil || report.Replay.StructuredResult.Value == nil || report.Replay.Run.WorkspaceId != spaces[0] || report.RequestSHA256 == "" {
		t.Fatalf("invalid report: %s", data)
	}
	if strings.Contains(string(data)+out+diagnostic, token) || !strings.Contains(out, `"type":"tool.completed"`) {
		t.Fatal("missing Tool evidence or exposed session")
	}
	request["mode"] = "autonomous"
	writeHeadlessJSON(t, requestPath, request)
	loopPath := filepath.Join(root, "loop.json")
	if code, output, info := invoke("start", "--request", requestPath, "--output", loopPath); code != 0 {
		t.Fatalf("bounded loop: %d %s %s", code, output, info)
	}
	// Same Run, not a resubmitted job, after a new client/process boundary.
	if code, exported, info := invoke("result", "--run", report.Replay.Run.Id); code != 0 || !strings.Contains(exported, report.Replay.Run.Id) {
		t.Fatalf("result: %d %s %s", code, exported, info)
	}
	if code, _, info := invoke("observe", "--run", report.Replay.Run.Id, "--after", "0"); code != 0 {
		t.Fatalf("observe: %d %s", code, info)
	}
	// Every auth failure passes through production middleware.
	for _, change := range []struct {
		name, flag, value string
		want              int
	}{{"foreign", "--workspace", foreign, 1}, {"expired", "--session-file", filepath.Join(root, "expired"), 4}} {
		if change.name == "expired" {
			if err := os.WriteFile(change.value, []byte("agentflow_session="+strings.Repeat("x", 43)), 0600); err != nil {
				t.Fatal(err)
			}
		}
		args := []string{"result", "--api-url", server.URL, "--workspace", spaces[0], "--session-file", session, "--run", report.Replay.Run.Id, change.flag, change.value}
		var output, info bytes.Buffer
		if code := runclient.Execute(t.Context(), args, &output, &info); code != change.want {
			t.Fatalf("%s: %d %s", change.name, code, &info)
		}
	}
	// Multi waits for explicit approval; the headless client never auto-continues.
	request["mode"], request["completion_contract"] = "multi_agent", nil
	writeHeadlessJSON(t, requestPath, request)
	waitingPath := filepath.Join(root, "waiting.json")
	if code, output, info := invoke("start", "--request", requestPath, "--output", waitingPath); code != 5 {
		t.Fatalf("waiting: %d %s %s", code, output, info)
	}
	var waiting runclient.Report
	bytesWaiting, _ := os.ReadFile(waitingPath)
	if err := json.Unmarshal(bytesWaiting, &waiting); err != nil {
		t.Fatal(err)
	}
	if waiting.Replay.Run.Status != "waiting_for_user" {
		t.Fatal("approval was not retained")
	}
	if code, output, info := invoke("cancel", "--run", waiting.Replay.Run.Id); code != 0 || !strings.Contains(output, `"status":"canceled"`) {
		t.Fatalf("cancel: %d %s %s", code, output, info)
	}
	// Verification failure must still export evidence, without an accepted object.
	request["mode"] = "single"
	request["message"] = "structured-output-gate-mismatch: invalid report"
	request["completion_contract"] = contractInput
	writeHeadlessJSON(t, requestPath, request)
	failedPath := filepath.Join(root, "failed.json")
	if code, output, info := invoke("start", "--request", requestPath, "--output", failedPath); code != 3 {
		t.Fatalf("verification failure: %d %s %s", code, output, info)
	}
	var failed runclient.Report
	failedBytes, _ := os.ReadFile(failedPath)
	if err := json.Unmarshal(failedBytes, &failed); err != nil {
		t.Fatal(err)
	}
	if failed.Replay.StructuredResult == nil || failed.Replay.StructuredResult.Value != nil {
		t.Fatal("provisional result exposed")
	}
	// Detached submission returns an identity while the real provider is blocked.
	// A second submission must use existing admission, not a client-side queue.
	request["message"], request["completion_contract"] = "stream-gate: hold until explicit cancellation", nil
	writeHeadlessJSON(t, requestPath, request)
	code, detached, info := invoke("start", "--request", requestPath, "--detach")
	if code != 0 {
		t.Fatalf("detach: %d %s %s", code, detached, info)
	}
	var submission struct {
		Receipt runclient.Receipt `json:"receipt"`
	}
	if err := json.Unmarshal([]byte(detached), &submission); err != nil || submission.Receipt.RunID == "" {
		t.Fatalf("receipt: %s", detached)
	}
	// Send a real SIGINT only after the child has emitted a snapshot, avoiding
	// process-startup timing assumptions. The server Run must remain active.
	observeArgs := append([]string{"observe"}, base...)
	observeArgs = append(observeArgs, "--run", submission.Receipt.RunID)
	observer := exec.CommandContext(t.Context(), binary, observeArgs...)
	pipe, err := observer.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var childDiagnostic bytes.Buffer
	observer.Stderr = &childDiagnostic
	if err := observer.Start(); err != nil {
		t.Fatal(err)
	}
	ready := make(chan struct{}, 1)
	go func() {
		scanner := bufio.NewScanner(pipe)
		scanner.Buffer(make([]byte, 4096), 2<<20)
		for scanner.Scan() {
			if strings.Contains(scanner.Text(), `"kind":"snapshot"`) {
				select {
				case ready <- struct{}{}:
				default:
				}
			}
		}
	}()
	select {
	case <-ready:
	case <-t.Context().Done():
		observer.Process.Kill()
		observer.Wait()
		t.Fatal("observer did not reach initial snapshot")
	}
	if err := observer.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	var childExit *exec.ExitError
	if err := observer.Wait(); !errors.As(err, &childExit) || childExit.ExitCode() != 130 {
		t.Fatalf("SIGINT: %v %s", err, &childDiagnostic)
	}
	if code, output, info := invoke("result", "--run", submission.Receipt.RunID); code != 5 {
		t.Fatalf("client interrupted server Run: %d %s %s", code, output, info)
	}
	if code, output, info := invoke("start", "--request", requestPath); code != 8 || !strings.Contains(info, "retry_after") {
		t.Fatalf("admission: %d %s %s", code, output, info)
	}
	if code, output, info := invoke("cancel", "--run", submission.Receipt.RunID); code != 0 {
		t.Fatalf("cancel running: %d %s %s", code, output, info)
	}
	if code, output, info := invoke("observe", "--run", submission.Receipt.RunID); code != 6 {
		t.Fatalf("observe canceled: %d %s %s", code, output, info)
	}
	// Restart the actual application and HTTP server against the same test DB.
	// No runtime state or client cursor is copied into the replacement instance.
	server.Close()
	if err := application.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	application, err = New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	server = httptest.NewServer(application)
	t.Cleanup(server.Close)
	base[1] = server.URL
	if code, output, info := invoke("observe", "--run", report.Replay.Run.Id, "--after", "1"); code != 0 {
		t.Fatalf("restart: %d %s %s", code, output, info)
	}
	// Repeatable evidence is retained by the normal backend CI artifact step.
	dir := os.Getenv("EVALUATION_REPORT_DIR")
	if dir == "" {
		dir = t.TempDir()
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	fileBytes, _ := os.ReadFile(fileEvidence)
	loopBytes, _ := os.ReadFile(loopPath)
	writeHeadlessJSON(t, filepath.Join(dir, "headless-client-evidence.json"), map[string]any{"schema": "headless-client-gate-v1", "command": "scripts/agentflow.sh start --workspace <owned-id> --request <same-request.json> --output <new-evidence.json>", "request": json.RawMessage(successfulInput), "file_request": fileRequest, "file_report": json.RawMessage(fileBytes), "successful_report": report, "loop_report": json.RawMessage(loopBytes), "waiting_run": waiting.Replay.Run.Id, "canceled_run": submission.Receipt.RunID, "verification_failure": failed, "checks": []string{"production OIDC session and Workspace enforcement", "real filesystem write/patch/read and Skill", "Single/Bounded loop accepted structured results and Multi approval pause", "durable observation/reload and application restart", "approval not auto-granted", "detach and admission rejection", "real SIGINT leaves server Run active", "explicit running cancellation", "failed verification retains evidence"}, "limitations": []string{"synthetic provider, no live model quality", "automatic reconnect transport faults covered separately; restart preserves completed evidence, not automatic active execution takeover"}})
}

func headlessFilesystemConfig(t *testing.T, cfg *config.Config, root, workspace string, storage *store.PostgresStore) {
	t.Helper()
	for _, dir := range []string{"project", "output", "skills/fixture-method"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "skills/fixture-method/SKILL.md"), []byte("---\nname: fixture-method\ndescription: Save a bounded report\n---\nSave and verify reports in the authorized output root."), 0600); err != nil {
		t.Fatal(err)
	}
	cfg.TrustedSkillDirectories = filepath.Join(root, "skills")
	cfg.FilesystemRootsPath = filepath.Join(root, "roots.json")
	writeHeadlessJSON(t, cfg.FilesystemRootsPath, map[string]any{"roots": []filesystem.RootConfig{{ID: "project", WorkspaceID: workspace, Path: filepath.Join(root, "project")}, {ID: "output", WorkspaceID: workspace, Path: filepath.Join(root, "output"), WriteAccess: "managed_output"}}})
	service, err := filesystem.New(cfg.FilesystemRootsPath, storage)
	if err != nil {
		t.Fatal(err)
	}
	tools := tool.DefaultConfig()
	for _, binding := range service.Bindings() {
		if binding.UnavailableReason != "" {
			continue
		}
		tools.EnabledTools = append(tools.EnabledTools, binding.Descriptor.Name)
		tools.SecurityPolicy.Rules = append(tools.SecurityPolicy.Rules, policy.Rule{ID: "headless-" + binding.Descriptor.Name, Tool: binding.Descriptor.Name, Action: policy.ActionAllowAndLog, Capability: binding.Descriptor.Security})
	}
	if err := tool.SaveConfig(cfg.ToolConfigPath, tools); err != nil {
		t.Fatal(err)
	}
}

func writeHeadlessJSON(t testing.TB, path string, value any) {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}
