package tools

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const mcpProtocolVersion = "2024-11-05"

type StdioMCPClient struct {
	mu       sync.Mutex
	sessions map[string]*mcpSession
}

func NewStdioMCPClient() *StdioMCPClient {
	return &StdioMCPClient{sessions: map[string]*mcpSession{}}
}

func (c *StdioMCPClient) ListTools(ctx context.Context, server MCPServerConfig) ([]MCPTool, error) {
	session, err := c.ensureSession(ctx, server)
	if err != nil {
		return nil, err
	}

	var result mcpListToolsResult
	if err := session.request(ctx, "tools/list", map[string]any{}, &result); err != nil {
		return nil, err
	}

	tools := make([]MCPTool, 0, len(result.Tools))
	for _, item := range result.Tools {
		tools = append(tools, MCPTool{
			Name:        item.Name,
			Description: item.Description,
			Parameters:  item.InputSchema,
		})
	}
	return tools, nil
}

func (c *StdioMCPClient) CallTool(ctx context.Context, serverID string, name string, args json.RawMessage) (any, error) {
	c.mu.Lock()
	session := c.sessions[serverID]
	c.mu.Unlock()
	if session == nil {
		return nil, fmt.Errorf("mcp server %q is not started", serverID)
	}

	params := map[string]any{"name": name}
	if len(bytes.TrimSpace(args)) > 0 {
		var decoded any
		if err := json.Unmarshal(args, &decoded); err != nil {
			return nil, fmt.Errorf("parse mcp tool arguments: %w", err)
		}
		params["arguments"] = decoded
	} else {
		params["arguments"] = map[string]any{}
	}

	var result mcpCallToolResult
	if err := session.request(ctx, "tools/call", params, &result); err != nil {
		return nil, err
	}
	if result.IsError {
		return result, errors.New(mcpContentText(result.Content))
	}
	return result, nil
}

func (c *StdioMCPClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	var errs []error
	for id, session := range c.sessions {
		if err := session.close(); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", id, err))
		}
	}
	c.sessions = map[string]*mcpSession{}
	return errors.Join(errs...)
}

func (c *StdioMCPClient) ensureSession(ctx context.Context, server MCPServerConfig) (*mcpSession, error) {
	if strings.TrimSpace(server.ID) == "" {
		return nil, errors.New("mcp server id is required")
	}
	if strings.TrimSpace(server.Transport) != "" && normalizeMCPTransport(server.Transport) != "stdio" {
		return nil, fmt.Errorf("mcp server %q transport %q is not supported", server.ID, server.Transport)
	}
	if strings.TrimSpace(server.Command) == "" {
		return nil, fmt.Errorf("mcp server %q command is required", server.ID)
	}

	fingerprint := serverFingerprint(server)

	c.mu.Lock()
	if existing := c.sessions[server.ID]; existing != nil {
		if existing.fingerprint == fingerprint && existing.isRunning() {
			c.mu.Unlock()
			return existing, nil
		}
		_ = existing.close()
		delete(c.sessions, server.ID)
	}
	c.mu.Unlock()

	session, err := startMCPSession(ctx, server, fingerprint)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	if existing := c.sessions[server.ID]; existing != nil {
		_ = session.close()
		if existing.fingerprint == fingerprint && existing.isRunning() {
			c.mu.Unlock()
			return existing, nil
		}
		_ = existing.close()
	}
	c.sessions[server.ID] = session
	c.mu.Unlock()
	return session, nil
}

type mcpSession struct {
	serverID    string
	fingerprint string
	cmd         *exec.Cmd
	stdin       io.WriteCloser
	responses   chan mcpRPCResponse
	done        chan error
	requestMu   sync.Mutex
	writeMu     sync.Mutex
	nextID      atomic.Int64
	closed      atomic.Bool
	exited      atomic.Bool
}

func startMCPSession(ctx context.Context, server MCPServerConfig, fingerprint string) (*mcpSession, error) {
	cmd := exec.CommandContext(ctx, server.Command, server.Args...)
	if strings.TrimSpace(server.WorkingDir) != "" {
		cmd.Dir = server.WorkingDir
	}
	if len(server.Env) > 0 {
		cmd.Env = append(os.Environ(), envPairs(server.Env)...)
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("create stdin for mcp server %q: %w", server.ID, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("create stdout for mcp server %q: %w", server.ID, err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("create stderr for mcp server %q: %w", server.ID, err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start mcp server %q: %w", server.ID, err)
	}

	session := &mcpSession{
		serverID:    server.ID,
		fingerprint: fingerprint,
		cmd:         cmd,
		stdin:       stdin,
		responses:   make(chan mcpRPCResponse, 32),
		done:        make(chan error, 1),
	}
	go session.readResponses(stdout)
	go session.drainStderr(stderr)
	go func() {
		err := cmd.Wait()
		session.exited.Store(true)
		session.done <- err
		close(session.done)
	}()

	initCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := session.initialize(initCtx); err != nil {
		_ = session.close()
		return nil, fmt.Errorf("initialize mcp server %q: %w", server.ID, err)
	}
	return session, nil
}

func (s *mcpSession) initialize(ctx context.Context) error {
	var result map[string]any
	params := map[string]any{
		"protocolVersion": mcpProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo": map[string]any{
			"name":    "agentflow-platform",
			"version": "0.1.0",
		},
	}
	if err := s.request(ctx, "initialize", params, &result); err != nil {
		return err
	}
	return s.notify("notifications/initialized", map[string]any{})
}

func (s *mcpSession) request(ctx context.Context, method string, params any, out any) error {
	s.requestMu.Lock()
	defer s.requestMu.Unlock()

	if !s.isRunning() {
		return fmt.Errorf("mcp server %q is not running", s.serverID)
	}
	id := s.nextID.Add(1)
	request := mcpRPCRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}
	if err := s.writeJSON(request); err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-s.done:
			if err == nil {
				return fmt.Errorf("mcp server %q exited", s.serverID)
			}
			return fmt.Errorf("mcp server %q exited: %w", s.serverID, err)
		case response, ok := <-s.responses:
			if !ok {
				return fmt.Errorf("mcp server %q response stream closed", s.serverID)
			}
			if response.ID == nil || *response.ID != id {
				continue
			}
			if response.Error != nil {
				return errors.New(response.Error.Message)
			}
			if out == nil {
				return nil
			}
			if len(response.Result) == 0 {
				return nil
			}
			if err := json.Unmarshal(response.Result, out); err != nil {
				return fmt.Errorf("parse mcp response for %s: %w", method, err)
			}
			return nil
		}
	}
}

func (s *mcpSession) notify(method string, params any) error {
	return s.writeJSON(mcpRPCRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	})
}

func (s *mcpSession) writeJSON(value any) error {
	bytes, err := json.Marshal(value)
	if err != nil {
		return err
	}
	bytes = append(bytes, '\n')

	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if _, err := s.stdin.Write(bytes); err != nil {
		return fmt.Errorf("write mcp request to %q: %w", s.serverID, err)
	}
	return nil
}

func (s *mcpSession) readResponses(stdout io.Reader) {
	defer close(s.responses)

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var response mcpRPCResponse
		if err := json.Unmarshal(line, &response); err != nil {
			log.Printf("mcp_server_stdout_parse_error server=%s error=%q line=%q", s.serverID, err, string(line))
			continue
		}
		s.responses <- response
	}
	if err := scanner.Err(); err != nil {
		log.Printf("mcp_server_stdout_error server=%s error=%q", s.serverID, err)
	}
}

func (s *mcpSession) drainStderr(stderr io.Reader) {
	scanner := bufio.NewScanner(stderr)
	scanner.Buffer(make([]byte, 0, 16*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			log.Printf("mcp_server_stderr server=%s line=%q", s.serverID, line)
		}
	}
}

func (s *mcpSession) isRunning() bool {
	if s.closed.Load() || s.exited.Load() {
		return false
	}
	return true
}

func (s *mcpSession) close() error {
	if !s.closed.CompareAndSwap(false, true) {
		return nil
	}
	_ = s.stdin.Close()
	if s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	select {
	case <-s.done:
	case <-time.After(2 * time.Second):
		return fmt.Errorf("timed out stopping mcp server %q", s.serverID)
	}
	return nil
}

type mcpRPCRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type mcpRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *mcpRPCError    `json:"error,omitempty"`
}

type mcpRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type mcpListToolsResult struct {
	Tools []struct {
		Name        string         `json:"name"`
		Description string         `json:"description"`
		InputSchema map[string]any `json:"inputSchema"`
	} `json:"tools"`
}

type mcpCallToolResult struct {
	Content []map[string]any `json:"content"`
	IsError bool             `json:"isError,omitempty"`
}

func mcpContentText(content []map[string]any) string {
	parts := make([]string, 0, len(content))
	for _, item := range content {
		if text, ok := item["text"].(string); ok && text != "" {
			parts = append(parts, text)
		}
	}
	if len(parts) == 0 {
		return "mcp tool call failed"
	}
	return strings.Join(parts, "\n")
}

func serverFingerprint(server MCPServerConfig) string {
	copy := server
	env := envPairs(copy.Env)
	return strings.Join([]string{
		copy.ID,
		copy.Transport,
		copy.Command,
		strings.Join(copy.Args, "\x00"),
		strings.Join(env, "\x00"),
		copy.URL,
		strings.Join(envPairs(copy.Headers), "\x00"),
		copy.WorkingDir,
	}, "\x1f")
}

func envPairs(env map[string]string) []string {
	pairs := make([]string, 0, len(env))
	for key, value := range env {
		pairs = append(pairs, key+"="+value)
	}
	sort.Strings(pairs)
	return pairs
}
