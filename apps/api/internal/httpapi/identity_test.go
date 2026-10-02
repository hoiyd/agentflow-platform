package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/identity"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/testsupport/pgfixture"
)

func TestAuthenticatedWorkspaceBoundary(t *testing.T) {
	dbURL := pgfixture.DatabaseURL(t)
	db, err := store.NewPostgresStore(dbURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.InitializeWorkspaceLifecycle(t.Context()); err != nil {
		t.Fatal(err)
	}
	var issuer string
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"issuer": issuer, "authorization_endpoint": issuer + "/authorize", "token_endpoint": issuer + "/token", "jwks_uri": issuer + "/keys", "id_token_signing_alg_values_supported": []string{"RS256"}})
	}))
	issuer = provider.URL
	t.Cleanup(provider.Close)
	workspaceID := pgfixture.GrantMemberships(t, dbURL, issuer, "operator", "workspace-a")[0]
	manager, err := identity.New(context.Background(), identity.Config{Mode: "oidc", Issuer: issuer, ClientID: "client", RedirectURL: "http://localhost:8080/api/auth/callback", WebURL: "http://localhost:3000"}, db)
	if err != nil {
		t.Fatal(err)
	}
	token := strings.Repeat("a", 43)
	hash := sha256.Sum256([]byte(token))
	if err := db.CreateSession(context.Background(), hex.EncodeToString(hash[:]), identity.UserID(issuer, "operator"), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	handler := &Handler{store: db, identity: manager, allowedOrigins: []string{"http://localhost:3000"}}
	routes := handler.Routes()
	for _, test := range []struct {
		name, method, path, workspace, cookie, origin, body string
		status                                              int
	}{
		{"anonymous", "GET", "/api/conversations", "workspace-a", "", "", "", 401},
		{"forged", "GET", "/api/conversations", "workspace-a", strings.Repeat("b", 43), "", "", 401},
		{"member", "GET", "/api/conversations", "workspace-a", token, "", "", 200},
		{"foreign", "GET", "/api/conversations", "workspace-b", token, "", "", 403},
		{"default-is-not-granted", "GET", "/api/conversations", "", token, "", "", 403},
		{"header-query-conflict", "GET", "/api/conversations?workspace_id=workspace-b", "workspace-a", token, "", "", 400},
		{"cross-origin-write", "POST", "/api/conversations", "workspace-a", token, "https://evil.invalid", `{"title":"blocked"}`, 403},
		{"missing-origin-write", "POST", "/api/conversations", "workspace-a", token, "", `{"title":"blocked"}`, 403},
		{"member-write", "POST", "/api/conversations", "workspace-a", token, "http://localhost:3000", `{"title":"allowed"}`, 201},
		{"anonymous-replay", "GET", "/api/runs/unknown/replay", "workspace-a", "", "", "", 401},
		{"foreign-events", "GET", "/api/runs/unknown/events", "workspace-b", token, "", "", 403},
		{"foreign-artifact", "GET", "/api/runs/unknown/artifacts/item", "workspace-b", token, "", "", 403},
		{"health-public", "GET", "/health", "", "", "", "", 200},
		{"session-public", "GET", "/api/auth/session", "", "", "", "", 200},
		{"preflight", "OPTIONS", "/api/conversations", "workspace-b", "", "http://localhost:3000", "", 204},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.workspace == "workspace-a" {
				test.workspace = workspaceID
			}
			r := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
			r.Header.Set(WorkspaceHeader, test.workspace)
			r.Header.Set("Origin", test.origin)
			r.Header.Set("X-Auth-User", "operator") // never establishes identity
			if test.cookie != "" {
				r.AddCookie(&http.Cookie{Name: "agentflow_session", Value: test.cookie})
			}
			w := httptest.NewRecorder()
			routes.ServeHTTP(w, r)
			if w.Code != test.status {
				t.Fatalf("status %d want %d: %s", w.Code, test.status, w.Body.String())
			}
			if test.origin == "http://localhost:3000" && w.Header().Get("Access-Control-Allow-Credentials") != "true" {
				t.Error("credentialed CORS missing")
			}
		})
	}
	// Authenticated scope is explicit even without a header, so payload cannot
	// select a different Workspace after the membership decision.
	r := httptest.NewRequest("POST", "/api/rag/search", nil)
	r.Header.Set(WorkspaceHeader, workspaceID)
	r.AddCookie(&http.Cookie{Name: "agentflow_session", Value: token})
	r.Header.Set("Origin", "http://localhost:3000")
	w := httptest.NewRecorder()
	handler.withIdentity(handler.withWorkspace(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := resolvePayloadWorkspace(r, "workspace-b"); ok {
			t.Fatal("body bypassed membership")
		}
		w.WriteHeader(204)
	}))).ServeHTTP(w, r)
	items, err := db.ListConversationsByWorkspace(workspaceID)
	if err != nil || len(items) != 1 || items[0].Title != "allowed" {
		t.Fatalf("rejected writes persisted: %v %v", items, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	routes.ServeHTTP(w, r)
	if w.Code != 503 {
		t.Fatalf("store failure must fail closed, got %d", w.Code)
	}
	t.Log(fmt.Sprintf(`identity_boundary_evidence={"cases":15,"store":"disposable_postgres","rejected_writes":0,"limitation":"membership only; no per-object ACL audit"}`))
}
