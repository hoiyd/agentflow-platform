package tool

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/netip"
	"net/url"
	"os"
	"regexp"
	"strings"
)

const (
	mcpMaxServers         = 8
	mcpMaxConfiguredTools = 64
	mcpMaxDiscoveredTools = 128
	mcpMaxResponseBytes   = 1 << 20
)

// ReadOnlyTools is an operator assertion after reviewing the server's behavior.
// Remote readOnlyHint/destructiveHint never changes this local allowlist.
type MCPServerConfig struct {
	ID             string   `json:"id"`
	URL            string   `json:"url"`
	ReadOnlyTools  []string `json:"read_only_tools"`
	BearerTokenEnv string   `json:"bearer_token_environment,omitempty"`
	Required       bool     `json:"required,omitempty"`
}

var mcpServerID = regexp.MustCompile(`^[a-z][a-z0-9-]{0,19}$`)
var mcpWorkspaceServerID = regexp.MustCompile(`^w[1-9][0-9]{0,18}$`)
var mcpRemoteName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,38}$`)
var mcpEnvironmentName = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

func loadMCPConfig(path string) ([]MCPServerConfig, error) {
	if strings.TrimSpace(path) == "" {
		return nil, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("read MCP configuration: %w", err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 64<<10+1))
	if err != nil || len(data) > 64<<10 {
		return nil, fmt.Errorf("MCP configuration exceeds 64 KiB or is unreadable")
	}
	var cfg struct {
		Servers []MCPServerConfig `json:"servers"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("invalid MCP configuration JSON")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, fmt.Errorf("MCP configuration has trailing data")
	}
	if len(cfg.Servers) > mcpMaxServers {
		return nil, fmt.Errorf("MCP configuration exceeds %d servers", mcpMaxServers)
	}
	seen, total := map[string]bool{}, 0
	for _, server := range cfg.Servers {
		if !mcpServerID.MatchString(server.ID) || mcpWorkspaceServerID.MatchString(server.ID) || seen[server.ID] {
			return nil, fmt.Errorf("invalid or duplicate MCP server ID")
		}
		seen[server.ID] = true
		u, err := url.Parse(server.URL)
		if err != nil || u.Hostname() == "" || u.User != nil || u.Opaque != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.ContainsAny(u.Host, "%*\\") {
			return nil, fmt.Errorf("MCP %s requires a secret-free HTTP endpoint", server.ID)
		}
		ip, _ := netip.ParseAddr(u.Hostname())
		local := u.Hostname() == "localhost" || ip.IsLoopback()
		if u.Scheme != "https" && !(u.Scheme == "http" && local) {
			return nil, fmt.Errorf("MCP %s requires HTTPS (HTTP is allowed only for explicit loopback)", server.ID)
		}
		if server.BearerTokenEnv != "" && !mcpEnvironmentName.MatchString(server.BearerTokenEnv) {
			return nil, fmt.Errorf("invalid MCP credential environment name")
		}
		if len(server.ReadOnlyTools) == 0 {
			return nil, fmt.Errorf("MCP %s requires reviewed read_only_tools", server.ID)
		}
		names := map[string]bool{}
		for _, name := range server.ReadOnlyTools {
			if !mcpRemoteName.MatchString(name) || names[name] {
				return nil, fmt.Errorf("invalid or duplicate MCP tool name for %s", server.ID)
			}
			names[name] = true
			total++
		}
	}
	if total > mcpMaxConfiguredTools {
		return nil, fmt.Errorf("MCP configuration exceeds %d tools", mcpMaxConfiguredTools)
	}
	return cfg.Servers, nil
}
