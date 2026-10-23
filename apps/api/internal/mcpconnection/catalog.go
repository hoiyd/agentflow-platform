package mcpconnection

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"slices"
	"time"

	"agentflow-platform/apps/api/internal/credential"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/tool"
	"agentflow-platform/apps/api/internal/tool/policy"
)

func (s *Service) adapter(c domain.MCPConnection) (*tool.MCPAdapter, error) {
	if err := s.validateURL(c.URL); err != nil {
		return nil, err
	}
	secret, err := s.decrypt(c)
	if err != nil {
		return nil, err
	}
	return s.newAdapter(c, credential.FromSecret(secret)), nil
}

func (s *Service) newAdapter(c domain.MCPConnection, key credential.Value) *tool.MCPAdapter {
	u, _ := url.Parse(c.URL)
	config := tool.MCPServerConfig{ID: "w" + c.ID, URL: c.URL, ReadOnlyTools: c.ReadOnlyTools}
	if c.AuthMode == "bearer" {
		config.BearerTokenEnv = "WORKSPACE_CREDENTIAL"
	}
	a := tool.NewWorkspaceMCPAdapter(config, key, s.local[u.Scheme+"://"+u.Host])
	// A ciphertext fingerprint identifies rotation without retaining token text
	// or a low-entropy token hash in the frozen model definition.
	sum := sha256.Sum256([]byte(c.CredentialCiphertext))
	a.SetCredentialReference(hex.EncodeToString(sum[:]))
	return a
}

func (s *Service) Catalog(workspace string, base *tool.Catalog) (*tool.Catalog, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connections, err := s.storage.ListMCPConnections(ctx, workspace)
	if err != nil {
		return nil, err
	}
	var bindings []tool.Binding
	var rules []policy.Rule
	for _, c := range connections {
		if !c.Enabled || c.Status != "discovered" {
			continue
		}
		items, err := s.bindings(c)
		if err != nil {
			return nil, err
		}
		for _, binding := range items {
			bindings = append(bindings, binding)
			rules = append(rules, policy.Rule{ID: "workspace-" + binding.Descriptor.Name, Tool: binding.Descriptor.Name, Action: policy.ActionAllowAndLog, Capability: binding.Descriptor.Security})
		}
	}
	catalog, err := base.CloneWith(bindings...)
	if err != nil {
		return nil, err
	}
	return catalog.WithRules(rules)
}

func (s *Service) bindings(c domain.MCPConnection) ([]tool.Binding, error) {
	identity, _ := json.Marshal(struct {
		URL, Auth, Credential string
		Selected              []string
		Definitions           []domain.MCPToolDefinition
	}{c.URL, c.AuthMode, c.CredentialCiphertext, c.ReadOnlyTools, c.Discovery})
	fingerprint := sha256.Sum256(identity)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, &Error{Status: 503, Message: "MCP connection manager is closed"}
	}
	if cached, ok := s.cache[c.ID]; ok && cached.fingerprint == fingerprint {
		return cached.bindings, nil
	}
	a, err := s.adapter(c)
	unavailable := ""
	if err != nil {
		// Optional credentials or a withdrawn local-origin exception must not
		// prevent ordinary Chat. Keep the dependency visible but fail closed.
		a = s.newAdapter(c, credential.Value{})
		unavailable = "credential_or_endpoint_unavailable"
	}
	if err = a.RestoreDefinitions(c.Discovery); err != nil {
		a.Close()
		return nil, err
	}
	bindings := a.Bindings()
	a.SetCallAuthorization(func(ctx context.Context, name string) error {
		if reason := s.authorize(ctx, c, name); reason != "" {
			return &tool.ExecutionError{Code: tool.ErrorExecutionUnavailable, Message: reason}
		}
		return nil
	})
	for i := range bindings {
		binding := &bindings[i]
		original := binding.Handler
		ready := binding.CheckAvailability
		name := binding.Descriptor.Source.RemoteName
		binding.UnavailableReason = unavailable
		binding.Handler = func(ctx context.Context, args json.RawMessage) (any, error) {
			if unavailable != "" {
				return nil, &tool.ExecutionError{Code: tool.ErrorExecutionUnavailable, Message: unavailable}
			}
			if reason := ready(); reason != "" {
				return nil, &tool.ExecutionError{Code: tool.ErrorExecutionUnavailable, Message: reason}
			}
			if reason := s.authorize(ctx, c, name); reason != "" {
				return nil, &tool.ExecutionError{Code: tool.ErrorExecutionUnavailable, Message: reason}
			}
			return original(ctx, args)
		}
	}
	if previous, ok := s.cache[c.ID]; ok {
		previous.adapter.Close()
	}
	s.cache[c.ID] = cachedConnection{fingerprint: fingerprint, adapter: a, bindings: bindings}
	return bindings, nil
}

func (s *Service) authorize(ctx context.Context, c domain.MCPConnection, name string) string {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	current, err := s.get(ctx, c.WorkspaceID, c.ID)
	if err != nil || !current.Enabled || current.Status != "discovered" || !slices.Contains(current.ReadOnlyTools, name) {
		return "mcp_connection_unavailable"
	}
	if err := s.storage.CheckWorkspaceExecution(c.WorkspaceID); err != nil {
		return "mcp_workspace_unavailable"
	}
	if current.URL != c.URL || current.AuthMode != c.AuthMode || current.CredentialCiphertext != c.CredentialCiphertext {
		return "mcp_definition_changed"
	}
	return ""
}
