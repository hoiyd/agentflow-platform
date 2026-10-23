// Package mcpconnection owns Workspace connection intent and credentials. MCP
// wire protocol and all actual execution remain in the shared Tool adapter.
package mcpconnection

import (
	"context"
	"crypto/cipher"
	"encoding/json"
	"errors"
	"net/netip"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/redaction"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/tool"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type Storage interface {
	ListMCPConnections(context.Context, string) ([]domain.MCPConnection, error)
	GetMCPConnection(context.Context, string, string) (domain.MCPConnection, error)
	SaveMCPConnection(context.Context, domain.MCPConnection) (domain.MCPConnection, error)
	DeleteMCPConnection(context.Context, string, string) error
	CheckWorkspaceExecution(string) error
}

type Config struct {
	EncryptionKey string
	// LocalOrigins is operator-only: user URL input never grants private access.
	LocalOrigins []string
}

type Service struct {
	storage Storage
	cipher  cipher.AEAD
	local   map[string]bool
	mu      sync.Mutex
	cache   map[string]cachedConnection
	closed  bool
	tests   chan struct{}
}

type cachedConnection struct {
	fingerprint [32]byte
	adapter     *tool.MCPAdapter
	bindings    []tool.Binding
}

type Input struct {
	Name          string
	URL           string
	AuthMode      string
	BearerToken   *string
	Enabled       bool
	ReadOnlyTools []string
	Revision      int64
}

type ToolView struct {
	Name        string `json:"name"`
	BindingName string `json:"binding_name"`
	Description string `json:"description"`
	Supported   bool   `json:"supported"`
	Reason      string `json:"reason,omitempty"`
}

type View struct {
	domain.MCPConnection
	CredentialConfigured bool       `json:"credential_configured"`
	Tools                []ToolView `json:"tools"`
}

type Error struct {
	Status  int
	Message string
}

func (e *Error) Error() string     { return e.Message }
func invalid(message string) error { return &Error{Status: 400, Message: message} }

func New(storage Storage, cfg Config) (*Service, error) {
	aead, err := newCipher(cfg.EncryptionKey)
	if err != nil {
		return nil, err
	}
	s := &Service{storage: storage, cipher: aead, local: map[string]bool{}, cache: map[string]cachedConnection{}, tests: make(chan struct{}, 4)}
	for _, origin := range cfg.LocalOrigins {
		u, err := url.Parse(strings.TrimSpace(origin))
		if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || (u.Scheme != "https" && u.Scheme != "http") {
			return nil, errors.New("MCP_WORKSPACE_LOCAL_ORIGINS requires exact HTTP origins")
		}
		s.local[u.Scheme+"://"+u.Host] = true
	}
	return s, nil
}

func (s *Service) validateURL(value string) error {
	if err := redaction.ValidateText(value); err != nil {
		return invalid("MCP endpoint URLs must not contain credentials")
	}
	u, err := url.Parse(value)
	if err != nil || u.Hostname() == "" || u.User != nil || u.Opaque != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.ContainsAny(u.Host, "%*\\") || len(value) > 2048 {
		return invalid("Use a secret-free Streamable HTTP endpoint URL")
	}
	if s.local[u.Scheme+"://"+u.Host] {
		return nil
	}
	if u.Scheme != "https" {
		return invalid("Workspace MCP connections require public HTTPS; private origins need operator approval")
	}
	ip, err := netip.ParseAddr(u.Hostname())
	if strings.EqualFold(u.Hostname(), "localhost") || err == nil && (!ip.Unmap().IsGlobalUnicast() || ip.Unmap().IsPrivate() || ip.Unmap().IsLoopback() || ip.Unmap().IsLinkLocalUnicast()) {
		return invalid("Private MCP endpoints require operator approval")
	}
	return nil
}

var decimalID = regexp.MustCompile(`^[1-9][0-9]{0,18}$`)

func (s *Service) get(ctx context.Context, workspace, id string) (domain.MCPConnection, error) {
	_, parseErr := strconv.ParseInt(id, 10, 64)
	if !decimalID.MatchString(id) || parseErr != nil {
		return domain.MCPConnection{}, store.ErrNotFound("MCP connection")
	}
	return s.storage.GetMCPConnection(ctx, workspace, id)
}

func view(c domain.MCPConnection) View {
	v := View{MCPConnection: c, CredentialConfigured: c.CredentialCiphertext != "", Tools: []ToolView{}}
	v.CredentialCiphertext = ""
	v.Discovery = nil
	for _, definition := range c.Discovery {
		var remote mcp.Tool
		if json.Unmarshal(definition.Definition, &remote) != nil {
			continue
		}
		v.Tools = append(v.Tools, ToolView{Name: remote.Name, BindingName: "mcp_w" + c.ID + "__" + remote.Name, Description: remote.Description, Supported: definition.UnavailableReason == "", Reason: definition.UnavailableReason})
	}
	return v
}

func (s *Service) Get(workspace, id string) (View, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := s.get(ctx, workspace, id)
	return view(c), err
}

func (s *Service) List(ctx context.Context, workspace string) ([]View, error) {
	items, err := s.storage.ListMCPConnections(ctx, workspace)
	if err != nil {
		return nil, err
	}
	views := make([]View, 0, len(items))
	for _, c := range items {
		views = append(views, view(c))
	}
	return views, nil
}

func (s *Service) Save(ctx context.Context, workspace, id string, input Input) (View, error) {
	if err := s.storage.CheckWorkspaceExecution(workspace); err != nil {
		return View{}, err
	}
	name := strings.TrimSpace(input.Name)
	if name == "" || len([]rune(name)) > 80 {
		return View{}, invalid("Connection name must contain 1 to 80 characters")
	}
	if err := redaction.ValidateValue(name); err != nil {
		return View{}, invalid("Connection name must not contain credentials")
	}
	if err := s.validateURL(input.URL); err != nil {
		return View{}, err
	}
	if input.AuthMode != "none" && input.AuthMode != "bearer" {
		return View{}, invalid("Supported authentication: none or bearer")
	}
	c := domain.MCPConnection{WorkspaceID: workspace, Status: "configured", ReadOnlyTools: []string{}, Discovery: []domain.MCPToolDefinition{}}
	if id != "" {
		var err error
		c, err = s.get(ctx, workspace, id)
		if err != nil {
			return View{}, err
		}
		if c.Revision != input.Revision {
			return View{}, &Error{Status: 409, Message: "Connection changed; reload before saving"}
		}
	}
	identityChanged := c.URL != input.URL || c.AuthMode != input.AuthMode || input.BearerToken != nil
	if identityChanged {
		c.Discovery = []domain.MCPToolDefinition{}
		c.Status = "configured"
		if c.URL != input.URL || input.AuthMode == "none" {
			c.CredentialCiphertext = ""
		}
	}
	c.Name, c.URL, c.AuthMode, c.Enabled = name, input.URL, input.AuthMode, input.Enabled
	if input.BearerToken != nil {
		secret := strings.TrimSpace(*input.BearerToken)
		if len(secret) > 8192 || strings.ContainsAny(secret, "\r\n\x00") {
			return View{}, invalid("Invalid Bearer token")
		}
		if secret != "" && c.AuthMode != "bearer" {
			return View{}, invalid("Choose Bearer authentication before setting a token")
		}
		c.CredentialCiphertext = ""
		if secret != "" {
			encrypted, err := s.encrypt(c, secret)
			if err != nil {
				return View{}, err
			}
			c.CredentialCiphertext = encrypted
		}
	}
	if c.AuthMode == "bearer" && c.CredentialCiphertext == "" {
		return View{}, invalid("A Bearer token is required")
	}
	selected := append([]string{}, input.ReadOnlyTools...)
	slices.Sort(selected)
	selected = slices.Compact(selected)
	if len(selected) > 64 {
		return View{}, invalid("At most 64 reviewed tools may be selected")
	}
	v := view(c)
	for _, name := range selected {
		if !slices.ContainsFunc(v.Tools, func(t ToolView) bool { return t.Name == name && t.Supported }) {
			return View{}, invalid("Test this connection and select only supported, reviewed read-only tools")
		}
	}
	if c.Enabled && (c.Status != "discovered" || len(selected) == 0) {
		return View{}, invalid("Test and select reviewed read-only tools before enabling")
	}
	c.ReadOnlyTools = selected
	saved, err := s.storage.SaveMCPConnection(ctx, c)
	return view(saved), err
}

func (s *Service) Test(ctx context.Context, workspace, id string) (View, error) {
	if err := s.storage.CheckWorkspaceExecution(workspace); err != nil {
		return View{}, err
	}
	c, err := s.get(ctx, workspace, id)
	if err != nil {
		return View{}, err
	}
	select {
	case s.tests <- struct{}{}:
		defer func() { <-s.tests }()
	default:
		return View{}, &Error{Status: 429, Message: "MCP connection tests are busy; try again shortly"}
	}
	a, err := s.adapter(c)
	if err != nil {
		return View{}, err
	}
	defer a.Close()
	c.Discovery, c.Status = a.DiscoverDefinitions(ctx)
	// A failed test withdraws availability but keeps the owner's chosen intent.
	saved, err := s.storage.SaveMCPConnection(ctx, c)
	return view(saved), err
}

func (s *Service) Delete(ctx context.Context, workspace, id string) error {
	if _, err := s.get(ctx, workspace, id); err != nil {
		return err
	}
	if err := s.storage.CheckWorkspaceExecution(workspace); err != nil {
		return err
	}
	err := s.storage.DeleteMCPConnection(ctx, workspace, id)
	if err == nil {
		s.mu.Lock()
		cached := s.cache[id]
		delete(s.cache, id)
		s.mu.Unlock()
		if cached.adapter != nil {
			cached.adapter.Close()
		}
	}
	return err
}

func (s *Service) Close() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	for _, cached := range s.cache {
		cached.adapter.Close()
	}
	s.cache = map[string]cachedConnection{}
}
