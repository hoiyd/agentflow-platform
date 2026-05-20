package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

type RoutedMCPClient struct {
	mu         sync.RWMutex
	transports map[string]string
	stdio      *StdioMCPClient
	http       *HTTPMCPClient
}

func NewRoutedMCPClient() *RoutedMCPClient {
	return &RoutedMCPClient{
		transports: map[string]string{},
		stdio:      NewStdioMCPClient(),
		http:       NewHTTPMCPClient(),
	}
}

func (c *RoutedMCPClient) ListTools(ctx context.Context, server MCPServerConfig) ([]MCPTool, error) {
	transport := normalizeMCPTransport(server.Transport)
	c.mu.Lock()
	c.transports[server.ID] = transport
	c.mu.Unlock()

	switch transport {
	case "", "stdio":
		return c.stdio.ListTools(ctx, server)
	case "http", "streamable-http":
		return c.http.ListTools(ctx, server)
	default:
		return nil, fmt.Errorf("mcp server %q transport %q is not supported", server.ID, server.Transport)
	}
}

func (c *RoutedMCPClient) CallTool(ctx context.Context, serverID string, name string, args json.RawMessage) (any, error) {
	c.mu.RLock()
	transport := c.transports[serverID]
	c.mu.RUnlock()

	switch transport {
	case "http", "streamable-http":
		return c.http.CallTool(ctx, serverID, name, args)
	default:
		return c.stdio.CallTool(ctx, serverID, name, args)
	}
}

func (c *RoutedMCPClient) Close() error {
	return c.stdio.Close()
}

func normalizeMCPTransport(transport string) string {
	return strings.ToLower(strings.TrimSpace(transport))
}
