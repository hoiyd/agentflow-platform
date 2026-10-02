package pgfixture

import (
	"database/sql"
	"testing"

	"agentflow-platform/apps/api/internal/identity"
)

// GrantMemberships seeds operator-owned grants in an already migrated disposable
// test database. Production startup and login never import or recreate these grants.
func GrantMemberships(t testing.TB, databaseURL, issuer, subject string, workspaces ...string) []string {
	t.Helper()
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	id := identity.UserID(issuer, subject)
	if _, err := db.ExecContext(t.Context(), `INSERT INTO auth_users (id,issuer,subject) VALUES ($1,$2,$3) ON CONFLICT DO NOTHING`, id, issuer, subject); err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, name := range workspaces {
		var workspace string
		if err := db.QueryRowContext(t.Context(), `INSERT INTO workspaces(owner_user_id,name) VALUES($1,$2) RETURNING id::text`, id, name).Scan(&workspace); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(t.Context(), `INSERT INTO auth_memberships (user_id,workspace_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, id, workspace); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, workspace)
	}
	return ids
}
