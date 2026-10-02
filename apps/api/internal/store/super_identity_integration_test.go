package store_test

import (
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/store"
)

// Failure inventory: orphaned owner/default/session references, restored revoked
// grants, archived write guards blocking metadata migration, conflicting reserved
// IDs, partial rename on failure, and duplicate Workspaces after restart.
func TestSuperIdentityRenamePreservesReferences(t *testing.T) {
	s, db, url := workspaceDatabase(t)
	if err := s.ApplyWorkspaceMigration(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	// Start from the previous layout with real owner FKs and write guards.
	if _, err := db.Exec(`DELETE FROM auth_users WHERE id IN('super','user_local');
	 INSERT INTO auth_users VALUES('user_local','agentflow:local','local','Local user');
	 INSERT INTO workspaces(id,owner_user_id,name) OVERRIDING SYSTEM VALUE VALUES(51,'user_local','Keep local space'),(52,'user_local','Keep revoked space');
	 INSERT INTO auth_memberships VALUES('user_local',51);
	 INSERT INTO auth_personal_workspaces(user_id,workspace_id) VALUES('user_local',51);
	 INSERT INTO workspace_migrations VALUES('previous-local',51,'user_local',NOW());
	 SELECT setval(pg_get_serial_sequence('workspaces','id'),52)`); err != nil {
		t.Fatal(err)
	}
	c, err := s.CreateConversationInWorkspace("52", "Archived content")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE conversations SET user_id='user_local' WHERE id=$1`, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE workspaces SET status='archived' WHERE id=52`); err != nil {
		t.Fatal(err)
	}
	if err = s.CreateSession(t.Context(), "reserved-session", "user_local", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	foreign := workspaceUser(t, s, "unaffected-owner")
	other, err := s.CreateWorkspace(t.Context(), foreign, "Other owner", "")
	if err != nil {
		t.Fatal(err)
	}
	var beforeFK, afterFK string
	const fkModes = `SELECT md5(string_agg(conname||condeferrable::text||condeferred::text,',' ORDER BY conname)) FROM pg_constraint WHERE contype='f' AND connamespace=(SELECT oid FROM pg_namespace WHERE nspname=current_schema())`
	if err = db.QueryRow(fkModes).Scan(&beforeFK); err != nil {
		t.Fatal(err)
	}
	if err = s.InitializeWorkspaceLifecycle(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(fkModes).Scan(&afterFK); err != nil || beforeFK != afterFK {
		t.Fatalf("FK modes changed: %s %s %v", beforeFK, afterFK, err)
	}
	var legacy, total int
	if err = db.QueryRow(`SELECT count(*) FROM auth_users WHERE id='user_local'`).Scan(&legacy); err != nil || legacy != 0 {
		t.Fatalf("legacy identity remains: %d %v", legacy, err)
	}
	u, err := s.SessionUser(t.Context(), "reserved-session")
	if err != nil || u.ID != "super" || u.Name != "Super" || u.Issuer != "agentflow:local" || u.Subject != "local" {
		t.Fatalf("session identity changed incorrectly: %+v %v", u, err)
	}
	if id, err := s.DefaultWorkspace(t.Context(), "super"); err != nil || id != "51" {
		t.Fatalf("default changed: %s %v", id, err)
	}
	if member, err := s.IsMember(t.Context(), "super", "52"); err != nil || member {
		t.Fatalf("revoked grant restored: %v %v", member, err)
	}
	var owner, user string
	if err = db.QueryRow(`SELECT owner_user_id FROM workspace_migrations WHERE legacy_id='previous-local'`).Scan(&owner); err != nil || owner != "super" {
		t.Fatalf("migration ownership: %s %v", owner, err)
	}
	if err = db.QueryRow(`SELECT user_id FROM conversations WHERE id=$1`, c.ID).Scan(&user); err != nil || user != "super" {
		t.Fatalf("archived resource reference: %s %v", user, err)
	}
	if _, err = db.Exec(`UPDATE conversations SET title='Forbidden' WHERE id=$1`, c.ID); err == nil {
		t.Fatal("archive write guard not restored")
	}
	if got, err := s.GetWorkspace(t.Context(), foreign, other.ID); err != nil || got.OwnerUserID != foreign {
		t.Fatalf("foreign owner changed: %+v %v", got, err)
	}
	if _, err = db.Exec(`INSERT INTO auth_memberships VALUES($1,51)`, foreign); err == nil {
		t.Fatal("owner FK lost")
	}
	if err = s.InitializeWorkspaceLifecycle(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT count(*) FROM workspaces WHERE owner_user_id='super'`).Scan(&total); err != nil || total != 2 {
		t.Fatalf("restart created duplicate spaces: %d %v", total, err)
	}
	// Restart through the same composition, including startup schema validation.
	reopened, err := store.NewPostgresStore(url)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err = reopened.InitializeWorkspaceLifecycle(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestSuperIdentityRejectsReservedCollision(t *testing.T) {
	s, db, _ := workspaceDatabase(t)
	if _, err := db.Exec(`INSERT INTO auth_users VALUES('user_local','agentflow:local','local','Local user'),('super','https://foreign.invalid','foreign','Foreign user')`); err != nil {
		t.Fatal(err)
	}
	if err := s.InitializeWorkspaceLifecycle(t.Context()); err == nil {
		t.Fatal("reserved super collision accepted")
	}
	var original int
	if err := db.QueryRow(`SELECT count(*) FROM auth_users WHERE id='user_local' AND issuer='agentflow:local' AND subject='local'`).Scan(&original); err != nil || original != 1 {
		t.Fatalf("failed migration changed original identity: %d %v", original, err)
	}
}

func TestSuperIdentityRejectsForeignReservedPrincipals(t *testing.T) {
	for _, id := range []string{"super", "user_local"} {
		t.Run(id, func(t *testing.T) {
			s, db, _ := workspaceDatabase(t)
			if _, err := db.Exec(`INSERT INTO auth_users VALUES($1,'https://foreign.invalid','foreign','Foreign user')`, id); err != nil {
				t.Fatal(err)
			}
			if err := s.InitializeWorkspaceLifecycle(t.Context()); err == nil {
				t.Fatal("foreign reserved identity accepted")
			}
			var issuer string
			if err := db.QueryRow(`SELECT issuer FROM auth_users WHERE id=$1`, id).Scan(&issuer); err != nil || issuer != "https://foreign.invalid" {
				t.Fatalf("foreign principal changed: %s %v", issuer, err)
			}
		})
	}
}

func TestSuperIdentityRenameRollsBackWithWorkspaceMigration(t *testing.T) {
	s, db, _ := workspaceDatabase(t)
	if _, err := db.Exec(`INSERT INTO auth_users VALUES('user_local','agentflow:local','local','Local user')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateConversationInWorkspace("unowned-legacy-space", "Must resolve owner"); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyWorkspaceMigration(t.Context(), nil); err == nil {
		t.Fatal("unresolved ownership accepted")
	}
	var old, renamed int
	if err := db.QueryRow(`SELECT count(*) FILTER(WHERE id='user_local'),count(*) FILTER(WHERE id='super') FROM auth_users`).Scan(&old, &renamed); err != nil || old != 1 || renamed != 0 {
		t.Fatalf("partial rename survived rollback: %d %d %v", old, renamed, err)
	}
}
