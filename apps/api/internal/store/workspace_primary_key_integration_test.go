package store_test

import (
	"encoding/json"
	"strings"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/store"
)

// Failure inventory: redundant identity, text-shaped FKs, lost IDs/content on
// upgrade, cross-owner grants, unsafe BIGINT serialization, and restart/rerun.
func assertWorkspacePrimaryKey(t *testing.T, s *store.PostgresStore) {
	t.Helper()
	plan, err := s.PreviewWorkspaceMigration(t.Context(), nil)
	if err != nil || len(plan) != 0 {
		t.Fatalf("unexpected legacy scope: %+v %v", plan, err)
	}
}

func TestWorkspaceUsesOnlyPrimaryKey(t *testing.T) {
	s, db, _ := workspaceDatabase(t)
	if err := s.InitializeWorkspaceLifecycle(t.Context()); err != nil {
		t.Fatal(err)
	}
	var redundant bool
	if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='workspaces' AND column_name='workspace_id')`).Scan(&redundant); err != nil || redundant {
		t.Fatalf("redundant Workspace identity: %v %v", redundant, err)
	}
	for _, table := range []string{"agents", "conversations", "messages", "runs", "collaboration_steps", "memories", "memory_changes", "memory_candidates", "documents", "task_state_revisions", "auth_memberships", "auth_personal_workspaces", "workspace_migrations"} {
		var kind string
		if err := db.QueryRow(`SELECT udt_name FROM information_schema.columns WHERE table_schema=current_schema() AND table_name=$1 AND column_name='workspace_id'`, table).Scan(&kind); err != nil || kind != "int8" {
			t.Fatalf("%s reference type: %s %v", table, kind, err)
		}
	}
	owner := workspaceUser(t, s, "primary-key-owner")
	w, err := s.CreateWorkspace(t.Context(), owner, "Primary key", "")
	if err != nil {
		t.Fatal(err)
	}
	var id string
	if err := db.QueryRow(`SELECT id::text FROM workspaces WHERE id=$1`, w.ID).Scan(&id); err != nil || id != w.ID {
		t.Fatalf("primary key mismatch: %s %+v %v", id, w, err)
	}
	for _, invalid := range []string{"missing", "0", "-1", "01", "9223372036854775808"} {
		if _, err := s.GetWorkspace(t.Context(), owner, invalid); !store.IsNotFound(err) {
			t.Fatalf("invalid ID %q did not return not-found: %v", invalid, err)
		}
		if _, err := s.UpdateWorkspace(t.Context(), owner, invalid, domain.WorkspaceUpdate{}, true); !store.IsNotFound(err) {
			t.Fatalf("invalid update ID %q: %v", invalid, err)
		}
	}
	assertWorkspacePrimaryKey(t, s)
}

func TestWorkspaceSchemaRejectsInvalidReferenceType(t *testing.T) {
	_, db, url := workspaceDatabase(t)
	if _, err := db.Exec(`ALTER TABLE auth_memberships ALTER COLUMN workspace_id TYPE boolean USING NULL::boolean`); err != nil {
		t.Fatal(err)
	}
	s, err := store.NewPostgresStore(url)
	if s != nil {
		s.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "auth_memberships.workspace_id") {
		t.Fatalf("invalid reference type accepted: %v", err)
	}
}

func TestWorkspaceGeneratedAliasUpgradePreservesData(t *testing.T) {
	s, db, url := workspaceDatabase(t)
	owner := workspaceUser(t, s, "alias-owner")
	foreign := workspaceUser(t, s, "alias-foreign")
	// Reproduce the previously shipped entity/foreign-key layout, without
	// depending on whether the current CREATE TABLE still defines the alias.
	if _, err := db.Exec(`ALTER TABLE workspaces ADD COLUMN IF NOT EXISTS workspace_id text GENERATED ALWAYS AS (id::text) STORED UNIQUE;
		ALTER TABLE workspaces ADD CONSTRAINT old_owner_alias UNIQUE(owner_user_id,workspace_id);
		INSERT INTO workspaces(id,owner_user_id,name) OVERRIDING SYSTEM VALUE VALUES(9007199254740993,'` + owner + `','Existing workspace');
		INSERT INTO auth_memberships(user_id,workspace_id) VALUES('` + owner + `','9007199254740993');
		INSERT INTO auth_personal_workspaces(user_id,workspace_id) VALUES('` + owner + `','9007199254740993');
		ALTER TABLE auth_memberships ADD CONSTRAINT old_owner_fk FOREIGN KEY(user_id,workspace_id) REFERENCES workspaces(owner_user_id,workspace_id)`); err != nil {
		t.Fatal(err)
	}
	c, err := s.CreateConversationInWorkspace("9007199254740993", "Keep identity")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`ALTER TABLE conversations ADD CONSTRAINT old_workspace_fk FOREIGN KEY(workspace_id) REFERENCES workspaces(workspace_id)`); err != nil {
		t.Fatal(err)
	}
	if err = s.ApplyWorkspaceMigration(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	w, err := s.GetWorkspace(t.Context(), owner, "9007199254740993")
	if err != nil || w.ID != "9007199254740993" || !w.IsDefault {
		t.Fatalf("upgrade changed entity/default: %+v %v", w, err)
	}
	encoded, _ := json.Marshal(w)
	var payload map[string]any
	_ = json.Unmarshal(encoded, &payload)
	if payload["id"] != "9007199254740993" {
		t.Fatalf("BIGINT API precision: %s", encoded)
	}
	if _, ok, err := s.GetConversationInWorkspace(w.ID, c.ID); err != nil || !ok {
		t.Fatalf("upgrade lost resource: %v %v", ok, err)
	}
	if _, err = db.Exec(`INSERT INTO auth_memberships(user_id,workspace_id) VALUES($1,$2)`, foreign, w.ID); err == nil {
		t.Fatal("upgrade lost owner FK")
	}
	if err = s.ApplyWorkspaceMigration(t.Context(), nil); err != nil {
		t.Fatalf("upgrade rerun: %v", err)
	}
	reopened, err := store.NewPostgresStore(url)
	if err != nil {
		t.Fatalf("upgraded schema restart: %v", err)
	}
	defer reopened.Close()
	if got, err := reopened.GetWorkspace(t.Context(), owner, w.ID); err != nil || got.ID != w.ID {
		t.Fatalf("reopened primary key: %+v %v", got, err)
	}
	var alias bool
	if err = db.QueryRow(`SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='workspaces' AND column_name='workspace_id')`).Scan(&alias); err != nil || alias {
		t.Fatalf("alias retained: %v %v", alias, err)
	}
	assertWorkspacePrimaryKey(t, reopened)
}
