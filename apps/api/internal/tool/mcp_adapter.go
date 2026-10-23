package tool

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// MCPAdapter discovers reviewed read-only definitions. Execution remains owned
// by Catalog/Executor; neither sessions nor credentials enter a frozen Run.
type MCPAdapter struct {
	mu              sync.RWMutex
	refreshMu       sync.Mutex
	endpoints       []*mcpEndpoint
	bindings        []Binding
	ctx             context.Context
	cancel          context.CancelFunc
	closed, started bool
	wg              sync.WaitGroup
}

func NewMCPAdapter(path string) (*MCPAdapter, error) {
	configs, err := loadMCPConfig(path)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	a := &MCPAdapter{ctx: ctx, cancel: cancel}
	for _, config := range configs {
		endpoint := newMCPEndpoint(config)
		a.endpoints = append(a.endpoints, endpoint)
		for _, name := range config.ReadOnlyTools {
			a.bindings = append(a.bindings, a.unavailable(endpoint, name, "mcp_discovery_pending"))
		}
	}
	return a, nil
}

func (a *MCPAdapter) Bindings() []Binding {
	a.mu.RLock()
	defer a.mu.RUnlock()
	result := make([]Binding, 0, len(a.bindings))
	for _, binding := range a.bindings {
		// Availability callbacks acquire this same lock; copy only values here.
		binding.Descriptor.Parameters = copySchema(binding.Descriptor.Parameters)
		binding.Descriptor.Source = copyToolSource(binding.Descriptor.Source)
		result = append(result, binding)
	}
	return result
}

func (a *MCPAdapter) CredentialScopes() []string {
	var scopes []string
	for _, endpoint := range a.endpoints {
		if endpoint.config.BearerTokenEnv != "" && endpoint.key.Available() {
			scopes = append(scopes, "mcp_"+endpoint.config.ID)
		}
	}
	return scopes
}

func (a *MCPAdapter) operationContext(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(parent, timeout)
	stop := context.AfterFunc(a.ctx, cancel)
	return ctx, func() { stop(); cancel() }
}

func (a *MCPAdapter) Refresh(ctx context.Context) {
	a.refreshMu.Lock()
	defer a.refreshMu.Unlock()
	if a.ctx.Err() != nil {
		return
	}
	var bindings []Binding
	for _, endpoint := range a.endpoints {
		bindings = append(bindings, a.discover(ctx, endpoint)...)
	}
	a.mu.Lock()
	a.bindings = bindings
	a.mu.Unlock()
}

// CheckRequired bounds startup only for explicitly required dependencies.
// Optional servers are first contacted asynchronously by Start.
func (a *MCPAdapter) CheckRequired(ctx context.Context) error {
	for _, endpoint := range a.endpoints {
		if !endpoint.config.Required {
			continue
		}
		validated := a.discover(ctx, endpoint)
		for _, binding := range validated {
			if binding.UnavailableReason != "" {
				return fmt.Errorf("required MCP server %s: %s", endpoint.config.ID, binding.UnavailableReason)
			}
		}
		a.mu.Lock()
		for index, current := range a.bindings {
			for _, binding := range validated {
				if current.Descriptor.Name == binding.Descriptor.Name {
					a.bindings[index] = binding
				}
			}
		}
		a.mu.Unlock()
	}
	return nil
}

func (a *MCPAdapter) Start(publish func([]Binding) error) {
	a.mu.Lock()
	if a.started || a.closed || len(a.endpoints) == 0 {
		a.mu.Unlock()
		return
	}
	a.started = true
	a.wg.Add(1)
	a.mu.Unlock()
	go func() {
		defer a.wg.Done()
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			a.Refresh(a.ctx)
			if a.ctx.Err() != nil {
				return
			}
			if err := publish(a.Bindings()); err != nil {
				log.Printf("MCP catalog refresh rejected: %v", err)
			}
			select {
			case <-a.ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

func (a *MCPAdapter) Close() {
	if a == nil {
		return
	}
	a.mu.Lock()
	a.closed = true
	a.cancel()
	a.mu.Unlock()
	a.wg.Wait()
	for _, endpoint := range a.endpoints {
		if closer, ok := endpoint.transport.(interface{ CloseIdleConnections() }); ok {
			closer.CloseIdleConnections()
		}
	}
}

func (a *MCPAdapter) discover(parent context.Context, endpoint *mcpEndpoint) []Binding {
	ctx, cancel := a.operationContext(parent, 5*time.Second)
	defer cancel()
	tools, reason, err := endpoint.discover(ctx)
	if err != nil {
		log.Printf("MCP discovery server=%s reason=%s error=%s", endpoint.config.ID, reason, endpoint.safeText(err.Error()))
	}
	bindings := make([]Binding, 0, len(endpoint.config.ReadOnlyTools))
	for _, name := range endpoint.config.ReadOnlyTools {
		if reason != "" {
			bindings = append(bindings, a.unavailable(endpoint, name, reason))
			continue
		}
		remote, ok := tools[name]
		if !ok {
			bindings = append(bindings, a.unavailable(endpoint, name, "mcp_tool_missing"))
			continue
		}
		binding, err := a.bindRemote(endpoint, remote)
		if err != nil {
			log.Printf("MCP schema rejected server=%s tool=%s error=%s", endpoint.config.ID, name, endpoint.safeText(err.Error()))
			binding = a.unavailable(endpoint, name, "mcp_schema_unsupported")
		}
		bindings = append(bindings, binding)
	}
	return bindings
}

type mcpDiscoveredTool struct {
	Tool            *mcp.Tool
	ProtocolVersion string
}

func (e *mcpEndpoint) discover(ctx context.Context) (map[string]mcpDiscoveredTool, string, error) {
	if e.config.BearerTokenEnv != "" && !e.key.Available() {
		return nil, "credential_unavailable", nil
	}
	session, err := e.connect(ctx)
	if err != nil {
		var auth *mcpAuthenticationError
		if errors.As(err, &auth) {
			return nil, "mcp_authentication_failed", err
		}
		return nil, "mcp_connection_failed", err
	}
	defer session.Close()
	tools, err := listMCPTools(ctx, session)
	if err != nil {
		return nil, "mcp_discovery_failed", err
	}
	return tools, "", nil
}

func listMCPTools(ctx context.Context, session *mcp.ClientSession) (map[string]mcpDiscoveredTool, error) {
	tools := map[string]mcpDiscoveredTool{}
	initialized := session.InitializeResult()
	if initialized == nil || initialized.ProtocolVersion == "" {
		return nil, fmt.Errorf("MCP negotiated protocol is missing")
	}
	for remote, err := range session.Tools(ctx, nil) {
		if err != nil {
			return nil, err
		}
		if remote == nil || remote.Name == "" || tools[remote.Name].Tool != nil || len(tools) >= mcpMaxDiscoveredTools {
			return nil, fmt.Errorf("invalid, duplicate or excessive MCP tools")
		}
		tools[remote.Name] = mcpDiscoveredTool{Tool: remote, ProtocolVersion: initialized.ProtocolVersion}
	}
	return tools, nil
}
