package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"sort"
	"time"

	"agentflow-platform/apps/api/internal/credential"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/egress"
	"agentflow-platform/apps/api/internal/redaction"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// NewWorkspaceMCPAdapter reuses the operator protocol adapter, but never infers
// private-network authority from a user URL. Callers validate persisted intent.
func NewWorkspaceMCPAdapter(config MCPServerConfig, key credential.Value, allowLocal bool) *MCPAdapter {
	ctx, cancel := context.WithCancel(context.Background())
	a := &MCPAdapter{ctx: ctx, cancel: cancel}
	e := newMCPEndpoint(config)
	e.key = key
	if !allowLocal {
		u, _ := url.Parse(config.URL)
		e.transport = egress.NewPublicTransport([]string{u.Scheme + "://" + u.Host})
	}
	a.endpoints = []*mcpEndpoint{e}
	return a
}

func (a *MCPAdapter) SetCredentialReference(reference string) {
	a.endpoints[0].credentialReference = reference
}

// Called at the actual tools/call boundary as well as the outer Binding entry.
func (a *MCPAdapter) SetCallAuthorization(authorize func(context.Context, string) error) {
	a.endpoints[0].authorize = authorize
}

// DiscoverDefinitions validates before persisting. Remote descriptions or hints
// cannot grant tools; explicit destructive hints can only narrow the selection.
func (a *MCPAdapter) DiscoverDefinitions(parent context.Context) ([]domain.MCPToolDefinition, string) {
	ctx, cancel := a.operationContext(parent, 5*time.Second)
	defer cancel()
	e := a.endpoints[0]
	tools, reason, _ := e.discover(ctx)
	definitions := []domain.MCPToolDefinition{}
	if reason != "" {
		return definitions, reason
	}
	names := make([]string, 0, len(tools))
	for name := range tools {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		remote := tools[name]
		if !mcpRemoteName.MatchString(name) {
			continue
		}
		encoded, err := json.Marshal(remote.Tool)
		if err != nil || len(encoded) > 128<<10 {
			continue
		}
		// Reject reflected credentials rather than retain a scrubbed executable schema.
		_, redactions, redactErr := redaction.JSON(encoded)
		if redactErr != nil || redactions > 0 {
			continue
		}
		if key := e.key.Reveal(); key != "" {
			quoted, _ := json.Marshal(key)
			if bytes.Contains(encoded, quoted[1:len(quoted)-1]) {
				continue
			}
		}
		definition := domain.MCPToolDefinition{ProtocolVersion: remote.ProtocolVersion, Definition: encoded}
		if _, err = a.bindRemote(e, remote); err != nil {
			definition.UnavailableReason = "mcp_schema_unsupported"
		}
		if remote.Tool.Annotations != nil && remote.Tool.Annotations.DestructiveHint != nil && *remote.Tool.Annotations.DestructiveHint {
			definition.UnavailableReason = "mcp_write_unsupported"
		}
		definitions = append(definitions, definition)
	}
	return definitions, "discovered"
}

// RestoreDefinitions never contacts the network. The ordinary Handler re-lists
// and compares the remote definition immediately before every actual call.
func (a *MCPAdapter) RestoreDefinitions(definitions []domain.MCPToolDefinition) error {
	var bindings []Binding
	e := a.endpoints[0]
	for _, name := range e.config.ReadOnlyTools {
		binding := a.unavailable(e, name, "mcp_tool_missing")
		for _, definition := range definitions {
			var remote mcp.Tool
			if err := json.Unmarshal(definition.Definition, &remote); err != nil {
				return err
			}
			if remote.Name != name {
				continue
			}
			if definition.UnavailableReason != "" {
				binding = a.unavailable(e, name, definition.UnavailableReason)
				break
			}
			ready, err := a.bindRemote(e, mcpDiscoveredTool{Tool: &remote, ProtocolVersion: definition.ProtocolVersion})
			if err != nil {
				return err
			}
			binding = ready
			break
		}
		bindings = append(bindings, binding)
	}
	a.mu.Lock()
	a.bindings = bindings
	a.mu.Unlock()
	return nil
}
