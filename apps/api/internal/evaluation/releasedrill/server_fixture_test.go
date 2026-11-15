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
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"agentflow-platform/apps/api/internal/redaction"
)

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

func (d *drill) createWorkspace(ctx context.Context) error {
	return d.step(ctx, "create_isolated_workspace", "POST /api/workspaces", "the local owner receives a persisted Workspace ID for all drill resources", func() (string, map[string]any, error) {
		payload := fmt.Sprintf(`{"name":%q}`, d.workspace)
		status, body, err := d.request(ctx, http.MethodPost, "/api/workspaces", payload)
		if err != nil {
			return "", nil, err
		}
		var workspace struct {
			ID string `json:"id"`
		}
		if status != http.StatusCreated || json.Unmarshal(body, &workspace) != nil || workspace.ID == "" {
			return fmt.Sprintf("http_status=%d", status), map[string]any{"response": safeText(string(body))}, errors.New("create drill Workspace failed")
		}
		d.workspace, d.report.WorkspaceID = workspace.ID, workspace.ID
		return "http_status=201", map[string]any{"workspace_id": workspace.ID}, nil
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
		var agent struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(body, &agent) != nil || agent.ID == "" {
			return "missing Agent identity", nil, errors.New("created Agent has no ID")
		}
		d.agentID = agent.ID
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

func commandOutput(ctx context.Context, dir string, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	return string(output), err
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
