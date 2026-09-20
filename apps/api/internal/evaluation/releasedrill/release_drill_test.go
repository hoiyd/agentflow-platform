package releasedrill

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"agentflow-platform/apps/api/internal/redaction"
)

const reportSchema = "agentflow-release-recovery-drill-v1"

type Options struct {
	RepoRoot    string
	DatabaseURL string
	ReportPath  string
	Output      io.Writer
}

type Report struct {
	SchemaVersion    string            `json:"schema_version"`
	Status           string            `json:"status"`
	GitRevision      string            `json:"git_revision"`
	WorkingTreeClean bool              `json:"working_tree_clean"`
	GoVersion        string            `json:"go_version"`
	StartedAt        time.Time         `json:"started_at"`
	CompletedAt      time.Time         `json:"completed_at"`
	Scope            string            `json:"scope"`
	WorkspaceID      string            `json:"workspace_id"`
	RunIDs           map[string]string `json:"run_ids"`
	Steps            []Step            `json:"steps"`
	Deferred         []DeferredItem    `json:"deferred"`
}

type Step struct {
	Name       string         `json:"name"`
	Status     string         `json:"status"`
	Command    string         `json:"command"`
	Expected   string         `json:"expected"`
	Observed   string         `json:"observed"`
	DurationMS int64          `json:"duration_ms"`
	Evidence   map[string]any `json:"evidence,omitempty"`
	Error      string         `json:"error,omitempty"`
}

type DeferredItem struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

type drill struct {
	options      Options
	report       Report
	apiRoot      string
	workspace    string
	agentID      string
	serverBin    string
	routeFile    string
	toolFile     string
	tempDir      string
	port         string
	provider     *modelStub
	providerHTTP *httptest.Server
	client       *http.Client
	process      *serverProcess
}

func Run(ctx context.Context, options Options) (report Report, resultErr error) {
	if strings.TrimSpace(options.DatabaseURL) == "" {
		return Report{}, errors.New("RELEASE_DRILL_DATABASE_URL or TEST_DATABASE_URL is required; use a disposable PostgreSQL database")
	}
	if options.Output == nil {
		options.Output = io.Discard
	}
	root, err := filepath.Abs(options.RepoRoot)
	if err != nil {
		return Report{}, err
	}
	options.RepoRoot = root
	if strings.TrimSpace(options.ReportPath) == "" {
		options.ReportPath = filepath.Join(root, ".cache", "release-drill", "latest.json")
	}

	revision, err := commandOutput(ctx, root, "git", "rev-parse", "HEAD")
	if err != nil {
		return Report{}, fmt.Errorf("read Git revision: %w", err)
	}
	status, err := commandOutput(ctx, root, "git", "status", "--porcelain", "--untracked-files=all", "--", ".", ":(exclude)docs/private")
	if err != nil {
		return Report{}, fmt.Errorf("read Git status: %w", err)
	}
	suffix := fmt.Sprintf("%x", time.Now().UnixNano())
	d := &drill{
		options: options, workspace: "release_drill_" + suffix, agentID: "agent_release_drill_" + suffix,
		client: &http.Client{},
		report: Report{
			SchemaVersion: reportSchema, Status: "running", GitRevision: strings.TrimSpace(revision),
			WorkingTreeClean: strings.TrimSpace(status) == "", GoVersion: runtime.Version(),
			StartedAt: time.Now().UTC(), Scope: "single_instance_local", WorkspaceID: "release_drill_" + suffix,
			RunIDs: map[string]string{}, Deferred: []DeferredItem{
				{Name: "backup_restore", Reason: "requires a deployment-owned backup target and restore database"},
				{Name: "previous_binary_forward_read", Reason: "requires a retained previous release binary and an explicit compatibility matrix"},
				{Name: "migration_rollback", Reason: "startup migrations are exercised; rollback policy remains deployment-specific"},
			},
		},
	}
	defer func() {
		if d.process != nil {
			d.process.kill()
		}
		if d.providerHTTP != nil {
			d.providerHTTP.Close()
		}
		if d.tempDir != "" {
			_ = os.RemoveAll(d.tempDir)
		}
		d.report.CompletedAt = time.Now().UTC()
		if resultErr == nil {
			d.report.Status = "passed"
		} else {
			d.report.Status = "failed"
		}
		if writeErr := writeReport(options.ReportPath, d.report); writeErr != nil {
			resultErr = errors.Join(resultErr, writeErr)
		}
		report = d.report
	}()

	if err := d.prepare(ctx); err != nil {
		return d.report, err
	}
	if err := d.run(ctx); err != nil {
		return d.report, err
	}
	return d.report, nil
}

func (d *drill) prepare(ctx context.Context) error {
	tempDir, err := os.MkdirTemp("", "agentflow-release-drill-")
	if err != nil {
		return err
	}
	d.tempDir = tempDir
	d.serverBin = filepath.Join(tempDir, "agentflow-server")
	d.routeFile = filepath.Join(tempDir, "model-routes.json")
	d.toolFile = filepath.Join(tempDir, "tools.json")
	port, err := freePort()
	if err != nil {
		return err
	}
	d.port, d.apiRoot = port, "http://127.0.0.1:"+port
	d.provider = newModelStub()
	d.providerHTTP = httptest.NewServer(d.provider)

	route := map[string]any{"routes": []map[string]any{{
		"id": "release_drill", "base_url": d.providerHTTP.URL + "/v1", "model": "release-drill-fixture",
		"credential_environment": "RELEASE_DRILL_PROVIDER_KEY", "request_timeout_seconds": 20,
		"capabilities":          map[string]bool{"tool_calling": true, "structured_output": true, "streaming": true},
		"context_window_tokens": 128000, "max_output_tokens": 8192, "priority": 100,
		"pricing": map[string]any{"source": "release_drill_fixture", "input_per_million_tokens_micros": 0, "output_per_million_tokens_micros": 0},
	}}}
	if err := writeJSONFile(d.routeFile, route); err != nil {
		return err
	}
	if err := writeJSONFile(d.toolFile, map[string]any{"enabled_tools": []string{"get_current_time"}}); err != nil {
		return err
	}

	return d.step(ctx, "build_server", "go build ./cmd/server", "server binary builds from the tested revision", func() (string, map[string]any, error) {
		cmd := exec.CommandContext(ctx, "go", "build", "-o", d.serverBin, "./cmd/server")
		cmd.Dir = filepath.Join(d.options.RepoRoot, "apps", "api")
		output, err := cmd.CombinedOutput()
		return "binary=" + filepath.Base(d.serverBin), map[string]any{"output": safeText(tail(string(output), 1000))}, err
	})
}

func (d *drill) run(ctx context.Context) error {
	if err := d.step(ctx, "startup_dependency_failure", "agentflow-server with DATABASE_URL unset", "process exits before readiness and reports the missing Postgres dependency", func() (string, map[string]any, error) {
		process, err := d.startServer("")
		if err != nil {
			return "", nil, err
		}
		exitErr := process.wait(5 * time.Second)
		if exitErr == nil {
			process.kill()
			return "process unexpectedly stayed available", nil, errors.New("server accepted an empty DATABASE_URL")
		}
		logs := safeText(tail(process.logs.String(), 1200))
		if !strings.Contains(logs, "DATABASE_URL is required") {
			return "process exited without the expected dependency error", map[string]any{"logs": logs}, errors.New("missing DATABASE_URL failure was not explicit")
		}
		return "non-zero exit before readiness", map[string]any{"logs": logs}, nil
	}); err != nil {
		return err
	}

	if err := d.startHealthyServer(ctx, "initial_readiness"); err != nil {
		return err
	}
	if err := d.createAgent(ctx); err != nil {
		return err
	}

	var drainedRun string
	if err := d.step(ctx, "inflight_request_drain", "SIGTERM agentflow-server while an Autonomous model call is in flight", "accepted Run completes and process exits cleanly", func() (string, map[string]any, error) {
		started := d.provider.delayNext(350 * time.Millisecond)
		session, err := d.startAutonomousRun(ctx, "Complete the release drain drill and return a concise result.")
		if err != nil {
			return "", nil, err
		}
		drainedRun = session.runID
		d.report.RunIDs["drained"] = drainedRun
		if err := waitSignal(ctx, started, 10*time.Second, "model request"); err != nil {
			return "model request was not reached", map[string]any{"run_id": drainedRun, "logs": safeText(tail(d.process.logs.String(), 1800))}, err
		}
		if err := d.process.terminate(); err != nil {
			return "", nil, err
		}
		if err := d.process.wait(15 * time.Second); err != nil {
			return "process did not drain cleanly", map[string]any{"run_id": drainedRun, "logs": safeText(tail(d.process.logs.String(), 1600))}, err
		}
		if err := session.wait(2 * time.Second); err != nil {
			return "HTTP stream did not finish cleanly", map[string]any{"run_id": drainedRun}, err
		}
		d.process = nil
		return "process exited 0 after the accepted Run finished", map[string]any{"run_id": drainedRun}, nil
	}); err != nil {
		return err
	}

	if err := d.startHealthyServer(ctx, "restart_after_drain"); err != nil {
		return err
	}
	if err := d.step(ctx, "drained_run_persisted", "GET /api/runs/{drained_run}/replay", "the completed Run is readable after process restart", func() (string, map[string]any, error) {
		replay, err := d.replay(ctx, drainedRun)
		if err != nil {
			return "", nil, err
		}
		if replay.Run.Status != "completed" {
			return "status=" + replay.Run.Status, map[string]any{"run_id": drainedRun}, fmt.Errorf("drained run status is %q", replay.Run.Status)
		}
		return "status=completed", map[string]any{"run_id": drainedRun, "event_count": len(replay.RunEvents)}, nil
	}); err != nil {
		return err
	}

	var crashedRun string
	if err := d.step(ctx, "worker_crash", "SIGKILL agentflow-server while a Stage checkpoint is executing", "process exits abruptly and leaves a durable running Run", func() (string, map[string]any, error) {
		started := d.provider.blockNext()
		session, err := d.startAutonomousRun(ctx, "Complete the crash recovery drill from the durable checkpoint.")
		if err != nil {
			return "", nil, err
		}
		crashedRun = session.runID
		d.report.RunIDs["crashed"] = crashedRun
		if err := waitSignal(ctx, started, 10*time.Second, "blocked model request"); err != nil {
			return "blocked model request was not reached", map[string]any{"run_id": crashedRun, "logs": safeText(tail(d.process.logs.String(), 1800))}, err
		}
		d.process.kill()
		_ = d.process.wait(5 * time.Second)
		_ = session.wait(2 * time.Second)
		d.process = nil
		time.Sleep(250 * time.Millisecond)
		return "process killed after checkpointed Stage start", map[string]any{"run_id": crashedRun}, nil
	}); err != nil {
		return err
	}

	if err := d.startHealthyServer(ctx, "restart_and_stale_repair"); err != nil {
		return err
	}
	if err := d.step(ctx, "stale_run_repair", "GET /api/runs/{crashed_run}/replay", "startup repair marks the stale Run failed_recoverable with checkpoint evidence", func() (string, map[string]any, error) {
		replay, err := d.replay(ctx, crashedRun)
		if err != nil {
			return "", nil, err
		}
		if err := validateRecoverableReplay(replay); err != nil {
			return "recovery evidence incomplete", map[string]any{
				"run_id": crashedRun, "checkpoint_statuses": checkpointStatuses(replay), "event_types": eventTypes(replay),
				"server_logs": safeText(tail(d.process.logs.String(), 1800)),
			}, err
		}
		return "status=failed_recoverable", map[string]any{
			"run_id": crashedRun, "checkpoint_statuses": checkpointStatuses(replay), "event_types": eventTypes(replay),
		}, nil
	}); err != nil {
		return err
	}

	if err := d.step(ctx, "checkpoint_resume", "POST /api/runs/{crashed_run}/resume", "Resume compensates the interrupted Stage and completes the original Run", func() (string, map[string]any, error) {
		status, body, err := d.request(ctx, http.MethodPost, "/api/runs/"+crashedRun+"/resume", `{"user_input":"Resume after the release drill crash."}`)
		if err != nil {
			return "", nil, err
		}
		if status != http.StatusOK || !bytes.Contains(body, []byte("event: done")) {
			return fmt.Sprintf("http_status=%d", status), map[string]any{"response": safeText(tail(string(body), 1200))}, errors.New("resume did not complete")
		}
		replay, err := d.replay(ctx, crashedRun)
		if err != nil {
			return "", nil, err
		}
		if err := validateCompletedReplay(replay); err != nil {
			return "resume evidence incomplete", map[string]any{"run_id": crashedRun}, err
		}
		return "status=completed", map[string]any{
			"run_id": crashedRun, "checkpoint_statuses": checkpointStatuses(replay), "event_types": eventTypes(replay),
		}, nil
	}); err != nil {
		return err
	}

	if err := d.step(ctx, "duplicate_resume_rejected", "POST /api/runs/{completed_run}/resume", "a repeated Resume fails with HTTP 409 and does not rerun work", func() (string, map[string]any, error) {
		status, body, err := d.request(ctx, http.MethodPost, "/api/runs/"+crashedRun+"/resume", `{}`)
		if err != nil {
			return "", nil, err
		}
		if status != http.StatusConflict {
			return fmt.Sprintf("http_status=%d", status), map[string]any{"response": safeText(string(body))}, errors.New("duplicate Resume was not rejected")
		}
		return "http_status=409", map[string]any{"run_id": crashedRun}, nil
	}); err != nil {
		return err
	}

	return d.step(ctx, "uncertain_tool_effect_guard", "go test ./internal/httpapi -run ^TestResumeRunRejectsStaleAndUnreconciledActions$ -count=1", "an unresolved external Tool effect blocks Resume", func() (string, map[string]any, error) {
		cmd := exec.CommandContext(ctx, "go", "test", "./internal/httpapi", "-run", "^TestResumeRunRejectsStaleAndUnreconciledActions$", "-count=1")
		cmd.Dir = filepath.Join(d.options.RepoRoot, "apps", "api")
		output, err := cmd.CombinedOutput()
		return "focused contract test executed", map[string]any{"evidence_source": "automated_contract_test", "output": safeText(tail(string(output), 1200))}, err
	})
}

func (d *drill) startHealthyServer(ctx context.Context, stepName string) error {
	return d.step(ctx, stepName, "start agentflow-server and GET /health", "startup migration/schema validation completes before readiness returns 200", func() (string, map[string]any, error) {
		process, err := d.startServer(d.options.DatabaseURL)
		if err != nil {
			return "", nil, err
		}
		d.process = process
		if err := d.waitReady(ctx, 15*time.Second); err != nil {
			return "readiness failed", map[string]any{"logs": safeText(tail(process.logs.String(), 1800))}, err
		}
		return "GET /health=200", map[string]any{"migration": "startup idempotent migration and schema validation completed"}, nil
	})
}

func (d *drill) createAgent(ctx context.Context) error {
	payload := fmt.Sprintf(`{"id":%q,"name":"Release Drill Agent","description":"Deterministic recovery drill agent.","system_prompt":"Return deterministic drill evidence.","tools":[],"memory_enabled":false,"retrieval_enabled":false}`, d.agentID)
	return d.step(ctx, "create_isolated_agent", "POST /api/agents", "a no-memory, no-retrieval Agent isolates the recovery protocol from external evidence", func() (string, map[string]any, error) {
		status, body, err := d.request(ctx, http.MethodPost, "/api/agents", payload)
		if err != nil {
			return "", nil, err
		}
		if status != http.StatusCreated {
			return fmt.Sprintf("http_status=%d", status), map[string]any{"response": safeText(string(body))}, errors.New("create drill agent failed")
		}
		return "http_status=201", map[string]any{"agent_id": d.agentID}, nil
	})
}

func (d *drill) startServer(databaseURL string) (*serverProcess, error) {
	values := map[string]string{
		"BIND_ADDRESS": "127.0.0.1", "PORT": d.port, "DATABASE_URL": databaseURL,
		"MODEL_ROUTE_CONFIG_PATH": d.routeFile, "RELEASE_DRILL_PROVIDER_KEY": "drill-only-not-a-secret",
		"EMBEDDING_BASE_URL": d.providerHTTP.URL + "/v1", "EMBEDDING_MODEL": "release-drill-embedding",
		"EMBEDDING_DIMENSIONS": "4", "TOOL_CONFIG_PATH": d.toolFile,
		"RECOVERY_STALE_RUN_TIMEOUT": "100ms", "AUTONOMOUS_MAX_ITERATIONS": "1",
		"CONTEXT_COMPACTION_MODE": "off", "MEMORY_ADAPTIVE_EXTRACTION_MODE": "off",
		"MODEL_RETRY_MAX_ATTEMPTS": "1", "MODEL_REQUEST_CAPTURE_MODE": "metadata_only",
	}
	cmd := exec.Command(d.serverBin)
	cmd.Dir = filepath.Dir(d.serverBin)
	cmd.Env = limitedEnv(values)
	logs := &lockedBuffer{}
	cmd.Stdout, cmd.Stderr = logs, logs
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	process := &serverProcess{cmd: cmd, logs: logs, done: make(chan error, 1)}
	go func() { process.done <- cmd.Wait() }()
	return process, nil
}

func (d *drill) waitReady(ctx context.Context, timeout time.Duration) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-d.process.done:
			return fmt.Errorf("server exited before readiness: %w", err)
		case <-deadline.C:
			return errors.New("readiness timeout")
		case <-ticker.C:
			request, _ := http.NewRequestWithContext(ctx, http.MethodGet, d.apiRoot+"/health", nil)
			response, err := d.client.Do(request)
			if err == nil {
				_ = response.Body.Close()
				if response.StatusCode == http.StatusOK {
					return nil
				}
			}
		}
	}
}

func (d *drill) startAutonomousRun(ctx context.Context, task string) (*runSession, error) {
	payload := fmt.Sprintf(`{"agent_id":%q,"message":%q,"mode":"autonomous","workspace_id":%q}`, d.agentID, task, d.workspace)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, d.apiRoot+"/api/chat", strings.NewReader(payload))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Workspace-ID", d.workspace)
	response, err := d.client.Do(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		_ = response.Body.Close()
		return nil, fmt.Errorf("start run: status=%d body=%s", response.StatusCode, safeText(string(body)))
	}
	scanner := bufio.NewScanner(response.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var event struct {
			RunID string `json:"run_id"`
		}
		if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event) == nil && event.RunID != "" {
			session := &runSession{runID: event.RunID, done: make(chan error, 1)}
			go func() {
				for scanner.Scan() {
				}
				scanErr := scanner.Err()
				_ = response.Body.Close()
				session.done <- scanErr
			}()
			return session, nil
		}
	}
	_ = response.Body.Close()
	return nil, fmt.Errorf("run stream ended before run_id: %w", scanner.Err())
}

type replay struct {
	Run struct {
		Status string `json:"status"`
	} `json:"run"`
	RunEvents []struct {
		Type string `json:"type"`
	} `json:"run_events"`
	StageCheckpoints []struct {
		Status string `json:"status"`
	} `json:"stage_checkpoints"`
	RecoverySummary map[string]any `json:"recovery_summary"`
}

func (d *drill) replay(ctx context.Context, runID string) (replay, error) {
	status, body, err := d.request(ctx, http.MethodGet, "/api/runs/"+runID+"/replay", "")
	if err != nil {
		return replay{}, err
	}
	if status != http.StatusOK {
		return replay{}, fmt.Errorf("get replay: status=%d body=%s", status, safeText(string(body)))
	}
	var result replay
	if err := json.Unmarshal(body, &result); err != nil {
		return replay{}, err
	}
	return result, nil
}

func validateRecoverableReplay(value replay) error {
	if value.Run.Status != "failed_recoverable" {
		return fmt.Errorf("status=%q, want failed_recoverable", value.Run.Status)
	}
	if len(value.StageCheckpoints) == 0 || value.RecoverySummary == nil {
		return errors.New("missing Stage checkpoint or recovery summary")
	}
	if !contains(eventTypes(value), "run.failed") || !contains(eventTypes(value), "stage.failed") {
		return errors.New("missing repaired terminal events")
	}
	return nil
}

func validateCompletedReplay(value replay) error {
	if value.Run.Status != "completed" {
		return fmt.Errorf("status=%q, want completed", value.Run.Status)
	}
	events := eventTypes(value)
	if !contains(events, "run.resumed") || !contains(events, "checkpoint.compensation_completed") || !contains(events, "run.completed") {
		return errors.New("missing Resume, compensation, or completion event")
	}
	return nil
}

func eventTypes(value replay) []string {
	result := make([]string, 0, len(value.RunEvents))
	for _, event := range value.RunEvents {
		result = append(result, event.Type)
	}
	return result
}

func checkpointStatuses(value replay) []string {
	result := make([]string, 0, len(value.StageCheckpoints))
	for _, checkpoint := range value.StageCheckpoints {
		result = append(result, checkpoint.Status)
	}
	return result
}

func (d *drill) request(ctx context.Context, method string, path string, body string) (int, []byte, error) {
	request, err := http.NewRequestWithContext(ctx, method, d.apiRoot+path, strings.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	request.Header.Set("X-Workspace-ID", d.workspace)
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := d.client.Do(request)
	if err != nil {
		return 0, nil, err
	}
	defer response.Body.Close()
	content, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	return response.StatusCode, content, err
}

func (d *drill) step(ctx context.Context, name, command, expected string, run func() (string, map[string]any, error)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	start := time.Now()
	observed, evidence, err := run()
	item := Step{Name: name, Status: "passed", Command: command, Expected: expected, Observed: observed,
		DurationMS: time.Since(start).Milliseconds(), Evidence: evidence}
	if err != nil {
		item.Status, item.Error = "failed", safeText(err.Error())
	}
	d.report.Steps = append(d.report.Steps, item)
	_, _ = fmt.Fprintf(d.options.Output, "[%s] %s: %s\n", item.Status, name, observed)
	if writeErr := writeReport(d.options.ReportPath, d.report); writeErr != nil {
		return writeErr
	}
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}

type serverProcess struct {
	cmd  *exec.Cmd
	logs *lockedBuffer
	done chan error
	once sync.Once
	err  error
}

func (p *serverProcess) terminate() error {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return nil
	}
	return p.cmd.Process.Signal(syscall.SIGTERM)
}

func (p *serverProcess) kill() {
	if p != nil && p.cmd != nil && p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
}

func (p *serverProcess) wait(timeout time.Duration) error {
	p.once.Do(func() {
		select {
		case p.err = <-p.done:
		case <-time.After(timeout):
			p.kill()
			p.err = errors.New("process exit timeout")
		}
	})
	return p.err
}

type runSession struct {
	runID string
	done  chan error
}

func (s *runSession) wait(timeout time.Duration) error {
	select {
	case err := <-s.done:
		return err
	case <-time.After(timeout):
		return errors.New("run stream timeout")
	}
}

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *lockedBuffer) Write(value []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(value)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

type modelGate struct {
	started chan struct{}
	release chan struct{}
	delay   time.Duration
}

type modelStub struct {
	mu   sync.Mutex
	next *modelGate
}

func newModelStub() *modelStub { return &modelStub{} }

func (s *modelStub) delayNext(delay time.Duration) <-chan struct{} {
	gate := &modelGate{started: make(chan struct{}), delay: delay}
	s.mu.Lock()
	s.next = gate
	s.mu.Unlock()
	return gate.started
}

func (s *modelStub) blockNext() <-chan struct{} {
	gate := &modelGate{started: make(chan struct{}), release: make(chan struct{})}
	s.mu.Lock()
	s.next = gate
	s.mu.Unlock()
	return gate.started
}

func (s *modelStub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/v1/chat/completions" {
		http.NotFound(w, r)
		return
	}
	var request struct {
		Stream   bool `json:"stream"`
		Messages []struct {
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	gate := s.next
	s.next = nil
	s.mu.Unlock()
	if gate != nil {
		close(gate.started)
		if gate.delay > 0 {
			select {
			case <-time.After(gate.delay):
			case <-r.Context().Done():
				return
			}
		} else {
			select {
			case <-gate.release:
			case <-r.Context().Done():
				return
			}
		}
	}
	content := "The deterministic release drill stage completed with sufficient information."
	if len(request.Messages) > 0 && strings.Contains(request.Messages[0].Content, "Return only valid JSON") {
		content = `{"decision":"stop","reason":"release drill evidence is complete","question":"","final_answer":"Release drill completed from durable state."}`
	}
	w.Header().Set("Content-Type", "application/json")
	if request.Stream {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q}}]}\n\ndata: {\"usage\":{\"prompt_tokens\":12,\"completion_tokens\":8,\"total_tokens\":20}}\n\ndata: [DONE]\n\n", content)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": content}}},
		"usage":   map[string]int{"prompt_tokens": 12, "completion_tokens": 8, "total_tokens": 20},
	})
}

func waitSignal(ctx context.Context, signal <-chan struct{}, timeout time.Duration, name string) error {
	select {
	case <-signal:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(timeout):
		return fmt.Errorf("timed out waiting for %s", name)
	}
}

func freePort() (string, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	defer listener.Close()
	return fmt.Sprintf("%d", listener.Addr().(*net.TCPAddr).Port), nil
}

func limitedEnv(values map[string]string) []string {
	for _, key := range []string{"PATH", "HOME", "TMPDIR", "LANG", "LC_ALL", "TZ", "SSL_CERT_FILE", "SSL_CERT_DIR"} {
		if value := os.Getenv(key); value != "" {
			values[key] = value
		}
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, key+"="+values[key])
	}
	return result
}

func writeJSONFile(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0600)
}

func writeReport(path string, report Report) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	temp := path + ".tmp"
	if err := os.WriteFile(temp, append(data, '\n'), 0600); err != nil {
		return err
	}
	return os.Rename(temp, path)
}

func commandOutput(ctx context.Context, dir string, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	return string(output), err
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func tail(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[len(value)-limit:]
}

func safeText(value string) string {
	cleaned, _ := redaction.Text(strings.TrimSpace(value))
	return cleaned
}
