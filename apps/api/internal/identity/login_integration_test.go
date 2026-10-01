package identity_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/identity"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/testsupport/pgfixture"
	"github.com/go-jose/go-jose/v4"
)

// Real discovery/JWKS/token transport and Postgres; only the identity provider
// is a fixture. Failures are injected at the protocol boundary, not the verifier.
func TestOIDCLoginMembershipLifecycle(t *testing.T) {
	t.Setenv("OIDC_CLIENT_SECRET", "fixture-only")
	dbURL := pgfixture.DatabaseURL(t)
	db, err := store.NewPostgresStore(dbURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var issuer, callback, nonce, challenge, lastState, failure string
	discoveryFailure := false
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			if discoveryFailure {
				http.Error(w, "fixture discovery failure", 503)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"issuer": issuer, "authorization_endpoint": issuer + "/authorize", "token_endpoint": issuer + "/token", "jwks_uri": issuer + "/keys", "id_token_signing_alg_values_supported": []string{"RS256"}})
		case "/keys":
			_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "fixture", Algorithm: "RS256", Use: "sig"}}})
		case "/authorize":
			if r.URL.Query().Get("prompt") != "login" {
				t.Error("authorization must request reauthentication, not silently reuse provider SSO")
				http.Error(w, "reauthentication required", http.StatusBadRequest)
				return
			}
			lastState = r.URL.Query().Get("state")
			nonce, challenge = r.URL.Query().Get("nonce"), r.URL.Query().Get("code_challenge")
			if nonce == "" || challenge == "" || r.URL.Query().Get("code_challenge_method") != "S256" {
				t.Error("missing nonce/PKCE")
			}
			http.Redirect(w, r, callback+"?code=fixture-code&state="+url.QueryEscape(r.URL.Query().Get("state")), http.StatusFound)
		case "/token":
			if failure == "exchange" {
				http.Error(w, "fixture exchange failure", 503)
				return
			}
			if failure == "missing-token" {
				_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "fixture-access", "token_type": "Bearer"})
				return
			}
			_ = r.ParseForm()
			hash := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
			if base64.RawURLEncoding.EncodeToString(hash[:]) != challenge || r.Form.Get("redirect_uri") != callback {
				t.Error("invalid serialized exchange")
			}
			tokenNonce, audience, expiry := nonce, "fixture-client", time.Now().Add(time.Hour).Unix()
			if failure == "nonce" {
				tokenNonce = "wrong"
			}
			if failure == "audience" {
				audience = "foreign-client"
			}
			if failure == "expiry" {
				expiry = time.Now().Add(-time.Hour).Unix()
			}
			tokenIssuer, subject := issuer, "operator"
			if failure == "issuer" {
				tokenIssuer = "https://foreign.invalid"
			}
			if failure == "subject" {
				subject = ""
			}
			signer, _ := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, (&jose.SignerOptions{}).WithHeader("kid", "fixture"))
			claims := map[string]any{"iss": tokenIssuer, "sub": subject, "aud": audience, "exp": expiry, "iat": time.Now().Unix(), "nonce": tokenNonce, "name": "Fixture operator", "email": "untrusted@example.invalid"}
			if failure == "extra-audience" {
				claims["aud"] = []string{audience, "foreign-client"}
				claims["azp"] = audience
			}
			if failure == "authorized-party" {
				claims["azp"] = "foreign-client"
			}
			payload, _ := json.Marshal(claims)
			if failure == "claims" {
				payload, _ = json.Marshal(map[string]any{"iss": tokenIssuer, "sub": subject, "aud": audience, "exp": expiry, "nonce": tokenNonce, "name": 123})
			}
			if failure == "long-name" {
				payload, _ = json.Marshal(map[string]any{"iss": tokenIssuer, "sub": subject, "aud": audience, "exp": expiry, "nonce": tokenNonce, "name": strings.Repeat("x", 300)})
			}
			signed, _ := signer.Sign(payload)
			token, _ := signed.CompactSerialize()
			if failure == "signature" {
				pieces := strings.Split(token, ".")
				pieces[2] = strings.Repeat("A", len(pieces[2]))
				token = strings.Join(pieces, ".")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "fixture-access", "token_type": "Bearer", "id_token": token})
		default:
			http.NotFound(w, r)
		}
	}))
	issuer = provider.URL
	t.Cleanup(provider.Close)
	pgfixture.GrantMemberships(t, dbURL, issuer, "operator", "workspace-a")
	var manager *identity.Manager
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mux := http.NewServeMux()
		manager.RegisterRoutes(mux)
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(api.Close)
	callback = api.URL + "/api/auth/callback"
	cfg := identity.Config{Mode: "oidc", Issuer: issuer, ClientID: "fixture-client", RedirectURL: callback, WebURL: "http://127.0.0.1:3000", SessionTTL: time.Hour}
	manager, err = identity.New(context.Background(), cfg, db)
	if err != nil {
		t.Fatal(err)
	}
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if req.URL.Host == "127.0.0.1:3000" {
			return http.ErrUseLastResponse
		}
		return nil
	}}
	login := func() int {
		response, err := client.Get(api.URL + "/api/auth/login")
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		return response.StatusCode
	}
	if got := login(); got != http.StatusSeeOther {
		t.Fatalf("login: %d", got)
	}
	// Even with the original state cookie copied, callback transactions are one-use.
	replay, _ := http.NewRequest("GET", callback+"?code=fixture-code&state="+lastState, nil)
	replay.AddCookie(&http.Cookie{Name: "agentflow_login", Value: lastState})
	replayed, err := (&http.Client{}).Do(replay)
	if err != nil {
		t.Fatal(err)
	}
	_ = replayed.Body.Close()
	if replayed.StatusCode != 400 {
		t.Fatal("replayed callback accepted")
	}
	request, _ := http.NewRequest(http.MethodGet, api.URL+"/api/conversations", nil)
	for _, cookie := range jar.Cookies(request.URL) {
		request.AddCookie(cookie)
	}
	user, err := manager.Authenticate(request)
	if err != nil || user.ID == "" {
		t.Fatalf("authenticate: %+v %v", user, err)
	}
	allowed, err := manager.IsMember(context.Background(), user.ID, "workspace-a")
	if err != nil || !allowed {
		t.Fatalf("membership: %t %v", allowed, err)
	}
	if allowed, _ := manager.IsMember(context.Background(), user.ID, "workspace-b"); allowed {
		t.Fatal("foreign workspace granted")
	}
	probe, err := client.Get(api.URL + "/api/auth/session")
	if err != nil {
		t.Fatal(err)
	}
	var info identity.SessionInfo
	if err := json.NewDecoder(probe.Body).Decode(&info); err != nil {
		t.Fatal(err)
	}
	_ = probe.Body.Close()
	if !info.Authenticated || len(info.Workspaces) != 1 || info.User.ID != user.ID {
		t.Fatalf("session info: %+v", info)
	}
	sqlDB, err := sql.Open("pgx", dbURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	var storedToken string
	if err := sqlDB.QueryRow(`SELECT token_hash FROM auth_sessions WHERE user_id=$1`, user.ID).Scan(&storedToken); err != nil || len(storedToken) != 64 {
		t.Fatalf("session hash: %v", err)
	}
	for _, c := range jar.Cookies(request.URL) {
		if storedToken == c.Value {
			t.Fatal("plaintext session stored")
		}
	}
	for _, column := range []struct{ table, name string }{{"auth_users", "issuer"}, {"auth_memberships", "workspace_id"}, {"auth_sessions", "expires_at"}, {"auth_login_attempts", "verifier"}} {
		var present bool
		if err := sqlDB.QueryRow(`SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_name=$1 AND column_name=$2)`, column.table, column.name).Scan(&present); err != nil || !present {
			t.Fatalf("startup migration missing %v: %v", column, err)
		}
	}
	// Restart with the same DB, then remove membership; the cookie is not a grant.
	manager, err = identity.New(context.Background(), cfg, db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Authenticate(request); err != nil {
		t.Fatal("restart lost session", err)
	}
	if allowed, _ := manager.IsMember(context.Background(), user.ID, "workspace-a"); !allowed {
		t.Fatal("restart replaced database membership")
	}
	if _, err := sqlDB.Exec(`DELETE FROM auth_memberships WHERE user_id=$1`, user.ID); err != nil {
		t.Fatal(err)
	}
	manager, err = identity.New(context.Background(), cfg, db)
	if err != nil {
		t.Fatal(err)
	}
	if allowed, _ := manager.IsMember(context.Background(), user.ID, "workspace-a"); allowed {
		t.Fatal("removed membership remained valid")
	}
	logout, _ := http.NewRequest(http.MethodPost, api.URL+"/api/auth/logout", nil)
	logout.Header.Set("Origin", cfg.WebURL)
	for _, cookie := range jar.Cookies(logout.URL) {
		logout.AddCookie(cookie)
	}
	response, err := client.Do(logout)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("logout: %d", response.StatusCode)
	}
	if _, err := manager.Authenticate(request); err == nil {
		t.Fatal("revoked session accepted")
	}
	cfg.AutoProvisionWorkspace = true
	manager, err = identity.New(context.Background(), cfg, db)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"nonce", "audience", "extra-audience", "authorized-party", "expiry", "issuer", "subject", "signature", "missing-token", "claims", "exchange"} {
		t.Run(kind, func(t *testing.T) {
			failure = kind
			want := http.StatusUnauthorized
			if kind == "exchange" {
				want = http.StatusBadGateway
			}
			if got := login(); got != want {
				t.Fatalf("bad token accepted: %d want %d", got, want)
			}
			var count int
			if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM auth_personal_workspaces`).Scan(&count); err != nil || count != 0 {
				t.Fatalf("invalid identity provisioned Workspace: %d %v", count, err)
			}
		})
	}
	cfg.AutoProvisionWorkspace = false
	manager, err = identity.New(context.Background(), cfg, db)
	if err != nil {
		t.Fatal(err)
	}
	failure = ""
	response, err = client.Get(callback + "?code=fixture-code&state=forged")
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatal("forged state accepted")
	}
	// Invalid configuration never degrades to trusted-local mode.
	for _, edit := range []func(*identity.Config){
		func(c *identity.Config) { c.Issuer = "http://untrusted.invalid" },
		func(c *identity.Config) { c.RedirectURL = "http://127.0.0.1:8080/wrong" },
		func(c *identity.Config) { c.WebURL = "http://localhost:3000" },
		func(c *identity.Config) { c.SessionTTL = -1 },
		func(c *identity.Config) { c.WebURL = "://invalid" },
	} {
		invalid := cfg
		edit(&invalid)
		if _, err := identity.New(context.Background(), invalid, db); err == nil {
			t.Fatal("invalid enabled config accepted")
		}
	}
	discoveryFailure = true
	if _, err := identity.New(context.Background(), cfg, db); err == nil {
		t.Fatal("discovery failure ignored")
	}
	discoveryFailure = false
	// Exercise transport handlers against real persistence with narrow faults,
	// retaining actual protocol verification and session management underneath.
	if got := login(); got != 303 {
		t.Fatal("baseline session for replacement faults failed", got)
	}
	for _, operation := range []string{"upsert", "create", "delete"} {
		manager, err = identity.New(context.Background(), cfg, &identityFaultStore{Store: db, operation: operation})
		if err != nil {
			t.Fatal(err)
		}
		if got := login(); got != 503 {
			t.Fatalf("%s failure did not fail closed: %d", operation, got)
		}
	}
	manager, err = identity.New(context.Background(), cfg, db)
	if err != nil {
		t.Fatal(err)
	}
	failure = "long-name"
	if got := login(); got != 303 {
		t.Fatal("long display name should be omitted", got)
	}
	failure = ""
	for _, operation := range []string{"session", "list"} {
		manager, err = identity.New(context.Background(), cfg, &identityFaultStore{Store: db, operation: operation})
		if err != nil {
			t.Fatal(err)
		}
		resp, err := client.Get(api.URL + "/api/auth/session")
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != 503 {
			t.Fatalf("%s error hidden", operation)
		}
	}
	manager, err = identity.New(context.Background(), cfg, &identityFaultStore{Store: db, operation: "delete"})
	if err != nil {
		t.Fatal(err)
	}
	for _, origin := range []string{cfg.WebURL, "https://foreign.invalid"} {
		r, _ := http.NewRequest("POST", api.URL+"/api/auth/logout", nil)
		r.Header.Set("Origin", origin)
		resp, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		want := 503
		if origin != cfg.WebURL {
			want = 403
		}
		if resp.StatusCode != want {
			t.Fatalf("logout failure status %d want %d", resp.StatusCode, want)
		}
	}
	manager, err = identity.New(context.Background(), cfg, db)
	if err != nil {
		t.Fatal(err)
	}
	// Secure deployments use host-only prefixed, HttpOnly, Lax cookies.
	secureConfig := cfg
	secureConfig.RedirectURL, secureConfig.WebURL = "https://localhost:8080/api/auth/callback", "https://localhost:3000"
	secureManager, err := identity.New(context.Background(), secureConfig, db)
	if err != nil {
		t.Fatal(err)
	}
	secureMux := http.NewServeMux()
	secureManager.RegisterRoutes(secureMux)
	recorder := httptest.NewRecorder()
	secureMux.ServeHTTP(recorder, httptest.NewRequest("GET", "/api/auth/login", nil))
	for _, c := range recorder.Result().Cookies() {
		if !c.Secure || !c.HttpOnly || c.Domain != "" || c.Path != "/" || c.Name != "__Host-agentflow_login" {
			t.Fatalf("insecure transaction cookie: %+v", c)
		}
	}
	_, _ = secureManager.Authenticate(httptest.NewRequest("GET", "/api/auth/session", nil))
	if got := login(); got != 303 {
		t.Fatal("replacement login failed", got)
	}
	if got := login(); got != 303 {
		t.Fatal("second login failed", got)
	}
	if _, err := sqlDB.Exec(`UPDATE auth_sessions SET expires_at=NOW()-INTERVAL '1 hour'`); err != nil {
		t.Fatal(err)
	}
	expiredResponse, err := client.Get(api.URL + "/api/auth/session")
	if err != nil {
		t.Fatal(err)
	}
	var expiredInfo identity.SessionInfo
	_ = json.NewDecoder(expiredResponse.Body).Decode(&expiredInfo)
	_ = expiredResponse.Body.Close()
	if expiredInfo.Authenticated {
		t.Fatal("expired persisted session authenticated")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if got := login(); got != 503 {
		t.Fatal("closed login storage did not fail closed", got)
	}
	t.Logf(`identity_evidence={"provider":"signed_oidc_fixture","store":"disposable_postgres","user_id":%q,"checks":["pkce","nonce","audience","expiry","membership","restart","removal","logout","forged_state"],"limitation":"not live-provider or full object authorization"}`, user.ID)
}

func TestAuthenticationConfigFailsClosed(t *testing.T) {
	for _, mode := range []string{"typo", "oidc"} {
		if _, err := identity.New(context.Background(), identity.Config{Mode: mode}, nil); err == nil {
			t.Fatalf("invalid %s accepted", mode)
		}
	}
	manager, err := identity.New(context.Background(), identity.Config{}, nil)
	if err != nil || manager != nil {
		t.Fatal("trusted local default changed", err)
	}
	mux := http.NewServeMux()
	manager.RegisterRoutes(mux)
	for _, path := range []string{"/api/auth/login", "/api/auth/register", "/api/auth/callback", "/api/auth/logout", "/api/auth/session"} {
		method := "GET"
		want := 404
		if path == "/api/auth/logout" {
			method = "POST"
		}
		if path == "/api/auth/session" {
			want = 200
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(method, path, nil))
		if w.Code != want {
			t.Fatalf("disabled route %s: %d", path, w.Code)
		}
	}
}

type identityFaultStore struct {
	identity.Store
	operation string
}

func (s *identityFaultStore) UpsertIdentity(ctx context.Context, u identity.User) error {
	if s.operation == "upsert" {
		return fmt.Errorf("fixture store unavailable")
	}
	return s.Store.UpsertIdentity(ctx, u)
}
func (s *identityFaultStore) CreateSession(ctx context.Context, hash, userID string, expires time.Time) error {
	if s.operation == "create" {
		return fmt.Errorf("fixture store unavailable")
	}
	return s.Store.CreateSession(ctx, hash, userID, expires)
}
func (s *identityFaultStore) DeleteSession(ctx context.Context, hash string) error {
	if s.operation == "delete" {
		return fmt.Errorf("fixture store unavailable")
	}
	return s.Store.DeleteSession(ctx, hash)
}
func (s *identityFaultStore) SessionUser(ctx context.Context, hash string) (identity.User, error) {
	if s.operation == "session" {
		return identity.User{}, fmt.Errorf("fixture store unavailable")
	}
	return s.Store.SessionUser(ctx, hash)
}
func (s *identityFaultStore) ListMemberships(ctx context.Context, userID string) ([]string, error) {
	if s.operation == "list" {
		return nil, fmt.Errorf("fixture store unavailable")
	}
	return s.Store.ListMemberships(ctx, userID)
}
