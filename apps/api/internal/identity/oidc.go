package identity

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/credential"
	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

type Config struct {
	Mode, Issuer, ClientID, RedirectURL, WebURL, MembershipPath string
	SessionTTL                                                  time.Duration
	// RegistrationEnabled requires an IdP supporting prompt=create and automatic onboarding.
	RegistrationEnabled, AutoProvisionWorkspace bool
}

type Manager struct {
	config   Config
	store    Store
	oauth    oauth2.Config
	verifier *oidc.IDTokenVerifier
	client   *http.Client
	secure   bool
	origin   string
}

// New fails closed on invalid enabled configuration. Empty/local mode exists
// solely for trusted development and never establishes authenticated identity.
func New(ctx context.Context, cfg Config, storage Store) (*Manager, error) {
	if cfg.Mode == "" || cfg.Mode == "local" {
		return nil, nil
	}
	if cfg.Mode != "oidc" {
		return nil, errors.New("AUTH_MODE must be local or oidc")
	}
	if storage == nil || cfg.ClientID == "" {
		return nil, errors.New("OIDC requires identity storage and client ID")
	}
	if cfg.RegistrationEnabled && !cfg.AutoProvisionWorkspace {
		return nil, errors.New("registration requires automatic personal Workspace provisioning")
	}
	for _, raw := range []string{cfg.Issuer, cfg.RedirectURL, cfg.WebURL} {
		if !safeURL(raw) {
			return nil, errors.New("OIDC URLs must use HTTPS (HTTP allowed only on loopback)")
		}
	}
	redirect, _ := url.Parse(cfg.RedirectURL)
	web, _ := url.Parse(cfg.WebURL)
	if redirect.Path != "/api/auth/callback" || redirect.RawQuery != "" || web.RawQuery != "" || web.Path != "" && web.Path != "/" {
		return nil, errors.New("invalid OIDC callback or frontend URL")
	}
	// SameSite=Lax cookies require a same-site frontend/API deployment. Ports may differ.
	if redirect.Scheme != web.Scheme || redirect.Hostname() != web.Hostname() {
		return nil, errors.New("frontend and OIDC callback must use the same scheme and hostname")
	}
	if cfg.SessionTTL == 0 {
		cfg.SessionTTL = 8 * time.Hour
	}
	if cfg.SessionTTL < time.Minute || cfg.SessionTTL > 24*time.Hour {
		return nil, errors.New("AUTH_SESSION_TTL must be between 1m and 24h")
	}
	client := &http.Client{Timeout: 10 * time.Second}
	ctx = oidc.ClientContext(ctx, client)
	provider, err := oidc.NewProvider(ctx, cfg.Issuer)
	if err != nil {
		return nil, errors.New("OIDC discovery failed")
	}
	endpoint := provider.Endpoint()
	if !safeURL(endpoint.AuthURL) || !safeURL(endpoint.TokenURL) {
		return nil, errors.New("OIDC endpoints must use HTTPS (HTTP allowed only on loopback)")
	}
	var metadata struct {
		JWKSURI string `json:"jwks_uri"`
	}
	if provider.Claims(&metadata) != nil || !safeURL(metadata.JWKSURI) {
		return nil, errors.New("OIDC key endpoint must use HTTPS (HTTP allowed only on loopback)")
	}
	cfg.WebURL = strings.TrimSuffix(cfg.WebURL, "/")
	if cfg.MembershipPath != "" {
		members, err := loadMembers(cfg.MembershipPath)
		if err != nil {
			return nil, err
		}
		if err := storage.ImportMemberships(ctx, cfg.Issuer, members); err != nil {
			return nil, errors.New("cannot import Workspace memberships")
		}
	}
	return &Manager{
		config: cfg, store: storage, client: client, secure: redirect.Scheme == "https", origin: web.Scheme + "://" + web.Host,
		oauth:    oauth2.Config{ClientID: cfg.ClientID, ClientSecret: credential.FromEnvironment("OIDC_CLIENT_SECRET").Reveal(), RedirectURL: cfg.RedirectURL, Endpoint: endpoint, Scopes: []string{oidc.ScopeOpenID, oidc.ScopeProfile}},
		verifier: provider.Verifier(&oidc.Config{ClientID: cfg.ClientID}),
	}, nil
}

func safeURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" {
		return false
	}
	return u.Scheme == "https" || u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1")
}

func (m *Manager) IsMember(ctx context.Context, userID, workspaceID string) (bool, error) {
	return m.store.IsMember(ctx, userID, workspaceID)
}

// Mutations require the exact configured frontend Origin, even when CORS would
// hide a response. Cookies are credentials; CORS alone does not prevent CSRF.
func (m *Manager) AllowedMutation(r *http.Request) bool {
	return r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions || r.Header.Get("Origin") == m.origin
}

var ErrUnauthenticated = errors.New("authentication required")

func (m *Manager) Authenticate(r *http.Request) (User, error) {
	cookie, err := r.Cookie(m.sessionCookieName())
	if err != nil || len(cookie.Value) != 43 || strings.TrimSpace(cookie.Value) != cookie.Value {
		return User{}, ErrUnauthenticated
	}
	user, err := m.store.SessionUser(r.Context(), digest(cookie.Value))
	if err == nil && user.Issuer != m.config.Issuer {
		return User{}, ErrUnauthenticated
	}
	return user, err
}
