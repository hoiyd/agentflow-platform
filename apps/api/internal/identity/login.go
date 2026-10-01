package identity

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

type SessionInfo struct {
	Mode              string   `json:"mode"`
	Authenticated     bool     `json:"authenticated"`
	User              *User    `json:"user"`
	Workspaces        []string `json:"workspaces"`
	PersonalWorkspace string   `json:"personal_workspace,omitempty"`
}

// RegisterRoutes is safe on a nil Manager, preserving trusted-local operation.
func (m *Manager) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/auth/session", m.session)
	mux.HandleFunc("GET /api/auth/login", m.login)
	mux.HandleFunc("GET /api/auth/register", m.register)
	mux.HandleFunc("GET /api/auth/callback", m.callback)
	mux.HandleFunc("POST /api/auth/logout", m.logout)
}

func authJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func authError(w http.ResponseWriter, status int, code, message string) {
	authJSON(w, status, map[string]any{"error": message, "code": code, "source": "identity", "category": "authentication", "retryable": status >= 500})
}

func (m *Manager) session(w http.ResponseWriter, r *http.Request) {
	info := SessionInfo{Mode: "local", Workspaces: []string{}}
	if m != nil {
		info.Mode = "oidc"
		user, err := m.Authenticate(r)
		if err != nil && !errors.Is(err, ErrUnauthenticated) {
			authError(w, 503, "identity_unavailable", "Identity storage is unavailable")
			return
		}
		if err == nil {
			info.User, info.Authenticated = &user, true
			info.Workspaces, err = m.store.ListMemberships(r.Context(), user.ID)
			if err != nil {
				authError(w, 503, "identity_unavailable", "Identity storage is unavailable")
				return
			}
			// Label only an authorized namespace, never advertise a revoked grant.
			for _, workspace := range info.Workspaces {
				if workspace == PersonalWorkspaceID(user.ID) {
					info.PersonalWorkspace = workspace
				}
			}
		}
	}
	authJSON(w, http.StatusOK, info)
}

func (m *Manager) login(w http.ResponseWriter, r *http.Request) {
	if m == nil {
		authError(w, 404, "login_disabled", "Login is disabled in trusted-local mode")
		return
	}
	m.startAuthorization(w, r, "login")
}

func (m *Manager) register(w http.ResponseWriter, r *http.Request) {
	if m == nil {
		authError(w, 404, "registration_disabled", "Registration is disabled")
		return
	}
	// Passwords, account creation and required actions remain entirely at the IdP.
	m.startAuthorization(w, r, "create")
}

func (m *Manager) startAuthorization(w http.ResponseWriter, r *http.Request, prompt string) {
	state, nonce, verifier := randomToken(), randomToken(), oauth2.GenerateVerifier()
	attempt := LoginAttempt{StateHash: digest(state), Nonce: nonce, Verifier: verifier, ExpiresAt: time.Now().Add(10 * time.Minute)}
	if err := m.store.SaveLoginAttempt(r.Context(), attempt); err != nil {
		authError(w, 503, "identity_unavailable", "Cannot start login")
		return
	}
	m.cookie(w, m.stateCookieName(), state, attempt.ExpiresAt)
	w.Header().Set("Cache-Control", "no-store")
	// Explicit sign-in requires reauthentication, not silent provider SSO reuse.
	// Reopening a tab uses the existing app session and never reaches this route.
	http.Redirect(w, r, m.oauth.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier), oauth2.SetAuthURLParam("prompt", prompt)), http.StatusFound)
}

func (m *Manager) callback(w http.ResponseWriter, r *http.Request) {
	if m == nil {
		authError(w, 404, "login_disabled", "Login is disabled in trusted-local mode")
		return
	}
	w.Header().Set("Referrer-Policy", "no-referrer")
	state := r.URL.Query().Get("state")
	cookie, err := r.Cookie(m.stateCookieName())
	m.clearCookie(w, m.stateCookieName())
	if err != nil || len(state) != 43 || subtle.ConstantTimeCompare([]byte(state), []byte(cookie.Value)) != 1 {
		authError(w, 400, "invalid_login_state", "Login state is invalid or expired")
		return
	}
	attempt, err := m.store.ConsumeLoginAttempt(r.Context(), digest(state))
	if err != nil {
		authError(w, 400, "invalid_login_state", "Login state is invalid or expired")
		return
	}
	if r.URL.Query().Get("error") != "" || r.URL.Query().Get("code") == "" {
		authError(w, 400, "login_rejected", "Login was not completed")
		return
	}
	ctx := oidc.ClientContext(r.Context(), m.client)
	token, err := m.oauth.Exchange(ctx, r.URL.Query().Get("code"), oauth2.VerifierOption(attempt.Verifier))
	if err != nil {
		authError(w, 502, "login_exchange_failed", "Identity provider exchange failed")
		return
	}
	raw, ok := token.Extra("id_token").(string)
	if !ok {
		authError(w, 401, "invalid_identity", "Identity provider did not return a valid identity")
		return
	}
	id, err := m.verifier.Verify(ctx, raw)
	if err != nil || id.Subject == "" || subtle.ConstantTimeCompare([]byte(id.Nonce), []byte(attempt.Nonce)) != 1 {
		authError(w, 401, "invalid_identity", "Identity provider did not return a valid identity")
		return
	}
	var claims struct {
		Name            string `json:"name"`
		AuthorizedParty string `json:"azp"`
	}
	if err := id.Claims(&claims); err != nil {
		authError(w, 401, "invalid_identity", "Invalid identity claims")
		return
	}
	// This client trusts only its own audience; no cross-client token sharing.
	if len(id.Audience) != 1 || claims.AuthorizedParty != "" && claims.AuthorizedParty != m.config.ClientID {
		authError(w, 401, "invalid_identity", "Identity provider did not return a valid identity")
		return
	}
	if len(claims.Name) > 256 {
		claims.Name = ""
	}
	user := User{ID: UserID(id.Issuer, id.Subject), Issuer: id.Issuer, Subject: id.Subject, Name: claims.Name}
	if err := m.store.UpsertIdentity(ctx, user); err != nil {
		authError(w, 503, "identity_unavailable", "Cannot create session")
		return
	}
	// Every verified OIDC identity gets one personal namespace. The durable marker
	// makes repeat logins idempotent without restoring a revoked Membership.
	if err := m.store.ProvisionPersonalWorkspace(ctx, user.ID); err != nil {
		authError(w, 503, "workspace_provisioning_failed", "Cannot open personal Workspace; sign in again to retry")
		return
	}
	value := randomToken()
	expires := minTime(time.Now().Add(m.config.SessionTTL), id.Expiry)
	if err := m.store.CreateSession(ctx, digest(value), user.ID, expires); err != nil {
		authError(w, 503, "identity_unavailable", "Cannot create session")
		return
	}
	// A successful login replaces/revokes any old browser session.
	if old, err := r.Cookie(m.sessionCookieName()); err == nil {
		if err := m.store.DeleteSession(ctx, digest(old.Value)); err != nil {
			_ = m.store.DeleteSession(ctx, digest(value))
			authError(w, 503, "identity_unavailable", "Cannot replace session")
			return
		}
	}
	m.cookie(w, m.sessionCookieName(), value, expires)
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, m.config.WebURL+"/workspace", http.StatusSeeOther)
}

func (m *Manager) logout(w http.ResponseWriter, r *http.Request) {
	if m == nil {
		authError(w, 404, "login_disabled", "Login is disabled in trusted-local mode")
		return
	}
	if !m.AllowedMutation(r) {
		authError(w, 403, "invalid_origin", "Request origin is not permitted")
		return
	}
	if cookie, err := r.Cookie(m.sessionCookieName()); err == nil {
		if err := m.store.DeleteSession(r.Context(), digest(cookie.Value)); err != nil {
			authError(w, 503, "identity_unavailable", "Cannot revoke session")
			return
		}
	}
	m.clearCookie(w, m.sessionCookieName())
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

func randomToken() string {
	var value [32]byte
	_, _ = rand.Read(value[:])
	return base64.RawURLEncoding.EncodeToString(value[:])
}

func digest(value string) string {
	hash := sha256.Sum256([]byte(value))
	return hex.EncodeToString(hash[:])
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func (m *Manager) sessionCookieName() string {
	if m.secure {
		return "__Host-agentflow_session"
	}
	return "agentflow_session"
}
func (m *Manager) stateCookieName() string {
	if m.secure {
		return "__Host-agentflow_login"
	}
	return "agentflow_login"
}

func (m *Manager) cookie(w http.ResponseWriter, name, value string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", HttpOnly: true, Secure: m.secure, SameSite: http.SameSiteLaxMode, Expires: expires, MaxAge: max(1, int(time.Until(expires).Seconds()))})
}

func (m *Manager) clearCookie(w http.ResponseWriter, name string) {
	http.SetCookie(w, &http.Cookie{Name: name, Path: "/", HttpOnly: true, Secure: m.secure, SameSite: http.SameSiteLaxMode, MaxAge: -1})
}
