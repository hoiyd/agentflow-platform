package tools

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type HTTPMCPClient struct {
	mu       sync.Mutex
	client   *http.Client
	sessions map[string]*httpMCPSession
}

func NewHTTPMCPClient() *HTTPMCPClient {
	return &HTTPMCPClient{
		client:   &http.Client{Timeout: 30 * time.Second},
		sessions: map[string]*httpMCPSession{},
	}
}

func (c *HTTPMCPClient) ListTools(ctx context.Context, server MCPServerConfig) ([]MCPTool, error) {
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

func (c *HTTPMCPClient) CallTool(ctx context.Context, serverID string, name string, args json.RawMessage) (any, error) {
	c.mu.Lock()
	session := c.sessions[serverID]
	c.mu.Unlock()
	if session == nil {
		return nil, fmt.Errorf("mcp http server %q is not initialized", serverID)
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

func (c *HTTPMCPClient) ensureSession(ctx context.Context, server MCPServerConfig) (*httpMCPSession, error) {
	if strings.TrimSpace(server.ID) == "" {
		return nil, errors.New("mcp server id is required")
	}
	if strings.TrimSpace(server.URL) == "" {
		return nil, fmt.Errorf("mcp http server %q url is required", server.ID)
	}

	fingerprint := serverFingerprint(server)
	c.mu.Lock()
	if existing := c.sessions[server.ID]; existing != nil && existing.fingerprint == fingerprint {
		c.mu.Unlock()
		return existing, nil
	}
	c.mu.Unlock()

	session := &httpMCPSession{
		serverID:    server.ID,
		fingerprint: fingerprint,
		url:         strings.TrimSpace(server.URL),
		headers:     server.Headers,
		client:      c.client,
	}
	initCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := session.initialize(initCtx); err != nil {
		return nil, fmt.Errorf("initialize mcp http server %q: %w", server.ID, err)
	}

	c.mu.Lock()
	c.sessions[server.ID] = session
	c.mu.Unlock()
	return session, nil
}

type httpMCPSession struct {
	serverID    string
	fingerprint string
	url         string
	headers     map[string]string
	sessionID   string
	client      *http.Client
	nextID      atomic.Int64
	requestMu   sync.Mutex
}

func (s *httpMCPSession) initialize(ctx context.Context) error {
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
	_ = s.notify(ctx, "notifications/initialized", map[string]any{})
	return nil
}

func (s *httpMCPSession) request(ctx context.Context, method string, params any, out any) error {
	s.requestMu.Lock()
	defer s.requestMu.Unlock()

	id := s.nextID.Add(1)
	request := mcpRPCRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}
	response, err := s.post(ctx, request)
	if err != nil {
		return err
	}
	if response.ID == nil || *response.ID != id {
		return fmt.Errorf("unexpected mcp http response id for %s", method)
	}
	if response.Error != nil {
		return errors.New(response.Error.Message)
	}
	if out == nil || len(response.Result) == 0 {
		return nil
	}
	if err := json.Unmarshal(response.Result, out); err != nil {
		return fmt.Errorf("parse mcp http response for %s: %w", method, err)
	}
	return nil
}

func (s *httpMCPSession) notify(ctx context.Context, method string, params any) error {
	_, err := s.post(ctx, mcpRPCRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	})
	return err
}

func (s *httpMCPSession) post(ctx context.Context, payload any) (mcpRPCResponse, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return mcpRPCResponse{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.url, bytes.NewReader(body))
	if err != nil {
		return mcpRPCResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", mcpProtocolVersion)
	if s.sessionID != "" {
		req.Header.Set("Mcp-Session-Id", s.sessionID)
	}
	for key, value := range s.headers {
		req.Header.Set(key, value)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return mcpRPCResponse{}, err
	}
	defer resp.Body.Close()

	if sessionID := resp.Header.Get("Mcp-Session-Id"); sessionID != "" {
		s.sessionID = sessionID
	}
	if resp.StatusCode == http.StatusAccepted || resp.StatusCode == http.StatusNoContent {
		return mcpRPCResponse{}, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bytes, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return mcpRPCResponse{}, fmt.Errorf("mcp http server %q returned %s: %s", s.serverID, resp.Status, strings.TrimSpace(string(bytes)))
	}

	return decodeHTTPMCPResponse(resp)
}

func decodeHTTPMCPResponse(resp *http.Response) (mcpRPCResponse, error) {
	contentType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	switch contentType {
	case "text/event-stream":
		return decodeSSEMCPResponse(resp.Body)
	default:
		var response mcpRPCResponse
		if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
			return mcpRPCResponse{}, err
		}
		return response, nil
	}
}

func decodeSSEMCPResponse(reader io.Reader) (mcpRPCResponse, error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	var dataLines []string
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if len(dataLines) > 0 {
				break
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if err := scanner.Err(); err != nil {
		return mcpRPCResponse{}, err
	}
	if len(dataLines) == 0 {
		return mcpRPCResponse{}, errors.New("empty mcp sse response")
	}

	var response mcpRPCResponse
	if err := json.Unmarshal([]byte(strings.Join(dataLines, "\n")), &response); err != nil {
		return mcpRPCResponse{}, err
	}
	return response, nil
}
