package store_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/identity"
	"agentflow-platform/apps/api/internal/store"
)

// Failure inventory: fresh databases requiring retired migration commands,
// redundant entity IDs, non-BIGINT references, retained migration bookkeeping,
// imprecise API IDs, foreign-owner grants, and changed defaults on restart.
func TestWorkspaceCurrentSchemaBootstrap(t *testing.T) {
	s, db, _ := workspaceDatabase(t)
	var redundant bool
	if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='workspaces' AND column_name='workspace_id') OR to_regclass('workspace_migrations') IS NOT NULL`).Scan(&redundant); err != nil || redundant {
		t.Fatalf("retired schema still created: %v %v", redundant, err)
	}
	for _, table := range []string{"agents", "conversations", "messages", "runs", "collaboration_steps", "memories", "memory_changes", "memory_candidates", "documents", "task_state_revisions", "auth_memberships", "auth_personal_workspaces"} {
		var kind string
		if err := db.QueryRow(`SELECT udt_name FROM information_schema.columns WHERE table_schema=current_schema() AND table_name=$1 AND column_name='workspace_id'`, table).Scan(&kind); err != nil || kind != "int8" {
			t.Fatalf("%s reference type: %s %v", table, kind, err)
		}
	}
	if id, err := s.DefaultWorkspace(t.Context(), identity.SuperUserID); err != nil || id == "" {
		t.Fatalf("bootstrap default missing: %s %v", id, err)
	}
}

func TestWorkspaceBootstrapPreservesDefaultAndRevocation(t *testing.T) {
	s, db, url := workspaceDatabase(t)
	ctx := t.Context()
	original, err := s.DefaultWorkspace(ctx, identity.SuperUserID)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.CreateSession(ctx, "local-session", identity.SuperUserID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	restarted, err := store.NewPostgresStore(url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restarted.Close() })
	if id, err := restarted.DefaultWorkspace(ctx, identity.SuperUserID); err != nil || id != original {
		t.Fatalf("default changed: %s %v", id, err)
	}
	if user, err := restarted.SessionUser(ctx, "local-session"); err != nil || user.ID != identity.SuperUserID || user.Name != "Super" {
		t.Fatalf("session changed: %+v %v", user, err)
	}
	if _, err := db.Exec(`DELETE FROM auth_memberships WHERE user_id=$1`, identity.SuperUserID); err != nil {
		t.Fatal(err)
	}
	if err := s.InitializeWorkspaceLifecycle(ctx); err != nil {
		t.Fatal(err)
	}
	if items, err := s.ListMemberships(ctx, identity.SuperUserID); err != nil || len(items) != 0 {
		t.Fatalf("revoked access restored: %v %v", items, err)
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM workspaces WHERE owner_user_id=$1`, identity.SuperUserID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("duplicate default: %d %v", count, err)
	}
}

func TestWorkspaceBootstrapRejectsForeignReservedIdentity(t *testing.T) {
	_, db, url := workspaceDatabase(t)
	if _, err := db.Exec(`UPDATE auth_users SET issuer='https://foreign.invalid',subject='foreign',name='Do not replace' WHERE id=$1`, identity.SuperUserID); err != nil {
		t.Fatal(err)
	}
	s, err := store.NewPostgresStore(url)
	if s != nil {
		s.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "reserved local identity") {
		t.Fatalf("foreign identity accepted: %v", err)
	}
	var name string
	if err := db.QueryRow(`SELECT name FROM auth_users WHERE id=$1`, identity.SuperUserID).Scan(&name); err != nil || name != "Do not replace" {
		t.Fatalf("foreign identity modified: %s %v", name, err)
	}
}

func TestWorkspaceSchemaRejectsLegacyReferenceType(t *testing.T) {
	_, db, url := workspaceDatabase(t)
	if _, err := db.Exec(`ALTER TABLE agents DROP CONSTRAINT agents_workspace_entity_fk; ALTER TABLE agents ALTER COLUMN workspace_id TYPE text USING workspace_id::text`); err != nil {
		t.Fatal(err)
	}
	s, err := store.NewPostgresStore(url)
	if s != nil {
		s.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "agents.workspace_id") {
		t.Fatalf("legacy reference type accepted: %v", err)
	}
}

func TestWorkspaceCurrentSchemaPreservesExactAPIIdentity(t *testing.T) {
	s, db, _ := workspaceDatabase(t)
	owner := workspaceUser(t, s, "exact-owner")
	if _, err := db.Exec(`INSERT INTO workspaces(id,owner_user_id,name) OVERRIDING SYSTEM VALUE VALUES(9007199254740993,$1,'Large identity')`, owner); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO auth_memberships VALUES($1,9007199254740993)`, owner); err != nil {
		t.Fatal(err)
	}
	w, err := s.GetWorkspace(t.Context(), owner, "9007199254740993")
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(w)
	var payload map[string]any
	_ = json.Unmarshal(encoded, &payload)
	if payload["id"] != "9007199254740993" {
		t.Fatalf("BIGINT API precision: %s", encoded)
	}
	for _, invalid := range []string{"missing", "0", "-1", "01", "9223372036854775808"} {
		if _, err := s.GetWorkspace(t.Context(), owner, invalid); err == nil {
			t.Fatalf("invalid ID accepted: %s", invalid)
		}
		if _, err := s.UpdateWorkspace(t.Context(), owner, invalid, domain.WorkspaceUpdate{}, true); err == nil {
			t.Fatalf("invalid mutation ID accepted: %s", invalid)
		}
	}
}
