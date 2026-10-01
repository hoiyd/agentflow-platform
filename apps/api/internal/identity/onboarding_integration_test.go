package identity_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"agentflow-platform/apps/api/internal/identity"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/testsupport/oidcfixture"
	"agentflow-platform/apps/api/internal/testsupport/pgfixture"
)

func TestMembershipImportDoesNotOverwriteDatabaseGrants(t *testing.T) {
	ctx := context.Background()
	db, err := store.NewPostgresStore(pgfixture.DatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	issuer := "https://identity.example/realm"
	if err := db.ImportMemberships(ctx, issuer, []identity.Member{{Subject: "operator", Workspaces: []string{"original"}}}); err != nil {
		t.Fatal(err)
	}
	if err := db.ImportMemberships(ctx, issuer, []identity.Member{{Subject: "operator", Workspaces: []string{"replacement"}}}); err != nil {
		t.Fatal(err)
	}
	items, err := db.ListMemberships(ctx, identity.UserID(issuer, "operator"))
	if err != nil || len(items) != 1 || items[0] != "original" {
		t.Fatalf("startup replaced database grants: %v, %v", items, err)
	}
}

func TestMembershipImportPreservesExistingIssuerOnUpgrade(t *testing.T) {
	ctx := context.Background()
	dbURL := pgfixture.DatabaseURL(t)
	legacy, err := sql.Open("pgx", dbURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = legacy.Close() })
	issuer := "https://existing.example/realm"
	user := identity.User{ID: identity.UserID(issuer, "revoked"), Issuer: issuer, Subject: "revoked"}
	// Start from the original schema, before onboarding/import records existed.
	for _, statement := range []string{
		`CREATE TABLE auth_users (id text PRIMARY KEY, issuer text NOT NULL, subject text NOT NULL, name text NOT NULL DEFAULT '', UNIQUE(issuer,subject))`,
		`CREATE TABLE auth_memberships (user_id text NOT NULL REFERENCES auth_users(id) ON DELETE CASCADE, workspace_id text NOT NULL, PRIMARY KEY(user_id,workspace_id))`,
	} {
		if _, err := legacy.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := legacy.Exec(`INSERT INTO auth_users(id,issuer,subject) VALUES($1,$2,$3)`, user.ID, issuer, user.Subject); err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Exec(`INSERT INTO auth_memberships(user_id,workspace_id) VALUES($1,'retained-grant')`, user.ID); err != nil {
		t.Fatal(err)
	}
	db, err := store.NewPostgresStore(dbURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.ImportMemberships(ctx, issuer, []identity.Member{{Subject: user.Subject, Workspaces: []string{"revoked-grant"}}}); err != nil {
		t.Fatal(err)
	}
	items, err := db.ListMemberships(ctx, user.ID)
	if err != nil || len(items) != 1 || items[0] != "retained-grant" {
		t.Fatalf("upgrade restored revoked grant: %v %v", items, err)
	}
}

func TestMembershipBootstrapFailureRollsBackAndRetries(t *testing.T) {
	ctx := context.Background()
	dbURL := pgfixture.DatabaseURL(t)
	db, err := store.NewPostgresStore(dbURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	sqlDB, err := sql.Open("pgx", dbURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	issuer := "https://bootstrap.example/realm"
	members := []identity.Member{{Subject: "one", Workspaces: []string{"accepted"}}, {Subject: "two", Workspaces: []string{"reject"}}}
	if _, err := sqlDB.Exec(`ALTER TABLE auth_memberships ADD CONSTRAINT fixture_reject CHECK(workspace_id <> 'reject')`); err != nil {
		t.Fatal(err)
	}
	if err := db.ImportMemberships(ctx, issuer, members); err == nil {
		t.Fatal("failed grant accepted")
	}
	for _, query := range []string{`SELECT COUNT(*) FROM auth_membership_imports WHERE issuer=$1`, `SELECT COUNT(*) FROM auth_users WHERE issuer=$1`} {
		var count int
		if err := sqlDB.QueryRow(query, issuer).Scan(&count); err != nil || count != 0 {
			t.Fatalf("partial bootstrap survived: %d %v", count, err)
		}
	}
	if _, err := sqlDB.Exec(`ALTER TABLE auth_memberships DROP CONSTRAINT fixture_reject`); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if err := db.ImportMemberships(ctx, issuer, members); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	for _, member := range members {
		items, err := db.ListMemberships(ctx, identity.UserID(issuer, member.Subject))
		if err != nil || len(items) != 1 || items[0] != member.Workspaces[0] {
			t.Fatalf("bootstrap retry: %v %v", items, err)
		}
	}
	if err := db.ProvisionPersonalWorkspace(ctx, "unverified-user"); err == nil {
		t.Fatal("unverified user received Workspace")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := db.ImportMemberships(ctx, issuer, members); err == nil {
		t.Fatal("closed database accepted bootstrap")
	}
	if err := db.ProvisionPersonalWorkspace(ctx, "unverified-user"); err == nil {
		t.Fatal("closed database accepted onboarding")
	}
}

func TestPersonalWorkspaceOnboardingLifecycle(t *testing.T) {
	ctx := context.Background()
	dbURL := pgfixture.DatabaseURL(t)
	db, err := store.NewPostgresStore(dbURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	sqlDB, err := sql.Open("pgx", dbURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	var manager *identity.Manager
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mux := http.NewServeMux()
		manager.RegisterRoutes(mux)
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(api.Close)
	provider := oidcfixture.New(t, api.URL+"/api/auth/callback")
	cfg := identity.Config{Mode: "oidc", Issuer: provider.Server.URL, ClientID: "fixture-client", RedirectURL: api.URL + "/api/auth/callback", WebURL: "http://127.0.0.1:3000", AutoProvisionWorkspace: true, RegistrationEnabled: true}
	manager, err = identity.New(ctx, cfg, db)
	if err != nil {
		t.Fatal(err)
	}
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, CheckRedirect: func(r *http.Request, _ []*http.Request) error {
		if strings.HasPrefix(r.URL.String(), cfg.WebURL) {
			return http.ErrUseLastResponse
		}
		return nil
	}}
	login := func(path string, want int) {
		t.Helper()
		response, err := client.Get(api.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != want {
			t.Fatalf("%s returned %d, want %d", path, response.StatusCode, want)
		}
	}
	login("/api/auth/register", 303)
	userID := identity.UserID(cfg.Issuer, "fixture-operator")
	workspace := identity.PersonalWorkspaceID(userID)
	items, err := db.ListMemberships(ctx, userID)
	if err != nil || len(items) != 1 || items[0] != workspace {
		t.Fatalf("onboarding: %v %v", items, err)
	}
	response, err := client.Get(api.URL + "/api/auth/session")
	if err != nil {
		t.Fatal(err)
	}
	var info identity.SessionInfo
	err = json.NewDecoder(response.Body).Decode(&info)
	response.Body.Close()
	if err != nil || !info.Authenticated || !info.RegistrationEnabled || info.PersonalWorkspace != workspace {
		t.Fatalf("session contract: %+v %v", info, err)
	}
	// Concurrent callbacks must share one durable marker and one grant.
	concurrentUser := identity.User{ID: identity.UserID(cfg.Issuer, "concurrent"), Issuer: cfg.Issuer, Subject: "concurrent"}
	if err := db.UpsertIdentity(ctx, concurrentUser); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if err := db.ProvisionPersonalWorkspace(ctx, concurrentUser.ID); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	var count int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM auth_personal_workspaces WHERE user_id=$1`, concurrentUser.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("duplicated marker: %d %v", count, err)
	}
	concurrentGrants, err := db.ListMemberships(ctx, concurrentUser.ID)
	if err != nil || len(concurrentGrants) != 1 {
		t.Fatalf("concurrent grants: %v %v", concurrentGrants, err)
	}
	if _, err := sqlDB.Exec(`DELETE FROM auth_memberships WHERE user_id=$1`, userID); err != nil {
		t.Fatal(err)
	}
	manager, err = identity.New(ctx, cfg, db)
	if err != nil {
		t.Fatal(err)
	}
	login("/api/auth/login", 303)
	items, err = db.ListMemberships(ctx, userID)
	if err != nil || len(items) != 0 {
		t.Fatalf("revoked access restored: %v %v", items, err)
	}
	// Real DB failure after marker INSERT must roll it back and issue no session.
	provider.SetSubject("retry-user")
	retryID := identity.UserID(cfg.Issuer, "retry-user")
	if _, err := sqlDB.Exec(`ALTER TABLE auth_memberships ADD CONSTRAINT fixture_reject CHECK (user_id <> '` + retryID + `')`); err != nil {
		t.Fatal(err)
	}
	login("/api/auth/register", 503)
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM auth_personal_workspaces WHERE user_id=$1`, retryID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed grant left marker: %d %v", count, err)
	}
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM auth_sessions WHERE user_id=$1`, retryID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed onboarding issued session: %d %v", count, err)
	}
	if _, err := sqlDB.Exec(`ALTER TABLE auth_memberships DROP CONSTRAINT fixture_reject`); err != nil {
		t.Fatal(err)
	}
	login("/api/auth/register", 303)
	items, err = db.ListMemberships(ctx, retryID)
	if err != nil || len(items) != 1 || items[0] == workspace {
		t.Fatalf("retry or isolation failed: %v %v", items, err)
	}
	cfg.RegistrationEnabled = false
	manager, err = identity.New(ctx, cfg, db)
	if err != nil {
		t.Fatal(err)
	}
	login("/api/auth/register", 404)
	cfg.RegistrationEnabled, cfg.AutoProvisionWorkspace = true, false
	if _, err := identity.New(ctx, cfg, db); err == nil {
		t.Fatal("registration without onboarding accepted")
	}
}
