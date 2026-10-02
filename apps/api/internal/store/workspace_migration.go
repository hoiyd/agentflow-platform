package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"

	"agentflow-platform/apps/api/internal/identity"
)

// The primary key is the only entity identity. API IDs are serialized as decimal
// strings; related tables store BIGINT foreign keys directly to this column.
const workspaceEntitySchema = `CREATE TABLE IF NOT EXISTS workspaces (
	 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
	 owner_user_id text NOT NULL REFERENCES auth_users(id),
	 name text NOT NULL CHECK(char_length(btrim(name)) BETWEEN 1 AND 80),
	 description text NOT NULL DEFAULT '' CHECK(char_length(description)<=2000),
	 status text NOT NULL DEFAULT 'active' CHECK(status IN('active','archived')),
	 created_at timestamptz NOT NULL DEFAULT NOW(),
	 updated_at timestamptz NOT NULL DEFAULT NOW(),
	 deleted_at timestamptz,
	 UNIQUE(owner_user_id,id)
)`

// Explicit inventory: child records without a namespace inherit scope through
// their existing Run/Conversation foreign keys. Captured prompts and immutable
// snapshots stay historical evidence; no arbitrary JSON/string replacement.
var workspaceReferenceTables = []string{"agents", "conversations", "messages", "runs", "collaboration_steps", "memories", "memory_changes", "memory_candidates", "documents", "task_state_revisions", "auth_memberships", "auth_personal_workspaces"}

type WorkspaceMigrationItem struct {
	LegacyID    string           `json:"legacy_id"`
	OwnerUserID string           `json:"owner_user_id,omitempty"`
	KnownOwners []string         `json:"known_owners"`
	Rows        map[string]int64 `json:"rows"`
	Problem     string           `json:"problem,omitempty"`
}

// OpenWorkspaceMigrationStore never runs startup DDL or seeds application data.
// Preview is genuinely read-only and can run before starting an upgraded API.
func OpenWorkspaceMigrationStore(ctx context.Context, dsn string) (*PostgresStore, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	if err = db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return &PostgresStore{db: db}, nil
}

type migrationQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func workspaceTables(ctx context.Context, q migrationQueryer) ([]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT table_name FROM information_schema.columns WHERE table_schema=current_schema() AND column_name='workspace_id' ORDER BY table_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tables := []string{}
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			return nil, err
		}
		if slices.Contains(workspaceReferenceTables, name) {
			tables = append(tables, name)
		}
	}
	return tables, rows.Err()
}

func (s *PostgresStore) PreviewWorkspaceMigration(ctx context.Context, owners map[string]string) ([]WorkspaceMigrationItem, error) {
	return previewWorkspaceMigration(ctx, s.db, owners)
}

func previewWorkspaceMigration(ctx context.Context, q migrationQueryer, owners map[string]string) ([]WorkspaceMigrationItem, error) {
	tables, err := workspaceTables(ctx, q)
	if err != nil {
		return nil, err
	}
	var entities bool
	if err = q.QueryRowContext(ctx, `SELECT to_regclass('workspaces') IS NOT NULL`).Scan(&entities); err != nil {
		return nil, err
	}
	items := map[string]*WorkspaceMigrationItem{}
	for _, table := range tables {
		filter := ""
		if entities {
			filter = ` AND workspace_id::text NOT IN(SELECT id::text FROM workspaces)`
		}
		rows, err := q.QueryContext(ctx, `SELECT workspace_id::text,count(*) FROM `+table+` WHERE workspace_id IS NOT NULL AND workspace_id::text<>''`+filter+` GROUP BY workspace_id`)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id string
			var count int64
			if err = rows.Scan(&id, &count); err != nil {
				rows.Close()
				return nil, err
			}
			if items[id] == nil {
				items[id] = &WorkspaceMigrationItem{LegacyID: id, KnownOwners: []string{}, Rows: map[string]int64{}}
			}
			items[id].Rows[table] = count
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	result := []WorkspaceMigrationItem{}
	for id, item := range items {
		for _, table := range []string{"auth_memberships", "auth_personal_workspaces"} {
			if !slices.Contains(tables, table) {
				continue
			}
			rows, err := q.QueryContext(ctx, `SELECT user_id FROM `+table+` WHERE workspace_id=$1`, id)
			if err != nil {
				return nil, err
			}
			for rows.Next() {
				var owner string
				if err = rows.Scan(&owner); err != nil {
					rows.Close()
					return nil, err
				}
				if !slices.Contains(item.KnownOwners, owner) {
					item.KnownOwners = append(item.KnownOwners, owner)
				}
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return nil, err
			}
		}
		slices.Sort(item.KnownOwners)
		item.OwnerUserID = owners[id]
		if item.OwnerUserID == "" && len(item.KnownOwners) == 1 {
			item.OwnerUserID = item.KnownOwners[0]
		}
		if item.OwnerUserID == "" {
			item.Problem = "Explicit owner required: legacy namespace has no unique owner"
		} else {
			var exists bool
			if err = q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM auth_users WHERE id=$1)`, item.OwnerUserID).Scan(&exists); err != nil {
				return nil, err
			}
			if !exists && item.OwnerUserID != identity.LocalUserID {
				item.Problem = "Owner does not exist in auth_users"
			}
		}
		result = append(result, *item)
	}
	slices.SortFunc(result, func(a, b WorkspaceMigrationItem) int { return strings.Compare(a.LegacyID, b.LegacyID) })
	for id, owner := range owners {
		if items[id] == nil {
			var history bool
			if err = q.QueryRowContext(ctx, `SELECT to_regclass('workspace_migrations') IS NOT NULL`).Scan(&history); err != nil {
				return nil, err
			}
			var matches bool
			if history {
				err = q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM workspace_migrations WHERE legacy_id=$1 AND owner_user_id=$2)`, id, owner).Scan(&matches)
				if err != nil {
					return nil, err
				}
			}
			if !matches {
				return nil, fmt.Errorf("owner mapping names an unknown or differently migrated namespace: %s", id)
			}
		}
	}
	return result, nil
}

// Offline and atomic. Table locks also prevent a still-running old binary from
// admitting a Run during the identity rewrite. Existing running Runs block apply.
func (s *PostgresStore) ApplyWorkspaceMigration(ctx context.Context, owners map[string]string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext('workspace-entity-migration'))`); err != nil {
		return err
	}
	tables, err := workspaceTables(ctx, tx)
	if err != nil {
		return err
	}
	if len(tables) > 0 {
		if _, err = tx.ExecContext(ctx, `LOCK TABLE `+strings.Join(tables, ",")+` IN EXCLUSIVE MODE`); err != nil {
			return err
		}
	}
	plan, err := previewWorkspaceMigration(ctx, tx, owners)
	if err != nil {
		return err
	}
	for _, item := range plan {
		if item.Problem != "" {
			return fmt.Errorf("workspace %s: %s", item.LegacyID, item.Problem)
		}
	}
	if len(plan) > 0 {
		var executing bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM runs WHERE status IN('running','queued','canceling'))`).Scan(&executing); err != nil {
			return err
		}
		if executing {
			return errors.New("stop the API and finish/cancel running and queued Runs before applying Workspace migration")
		}
	}
	if _, err = tx.ExecContext(ctx, workspaceEntitySchema); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS workspace_migrations(legacy_id text PRIMARY KEY,workspace_id bigint NOT NULL REFERENCES workspaces(id),owner_user_id text NOT NULL REFERENCES auth_users(id),migrated_at timestamptz NOT NULL DEFAULT NOW())`); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO auth_users(id,issuer,subject,name) VALUES($1,'agentflow:local','local','Local user') ON CONFLICT DO NOTHING`, identity.LocalUserID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `ALTER TABLE auth_personal_workspaces ALTER COLUMN workspace_id DROP NOT NULL`); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `CREATE TEMP TABLE workspace_id_mapping(legacy_id text PRIMARY KEY,workspace_id text NOT NULL UNIQUE,owner_user_id text NOT NULL) ON COMMIT DROP`); err != nil {
		return err
	}
	for _, item := range plan {
		name := item.LegacyID
		if name == "default_workspace" || name == "default" {
			name = "Default workspace"
		} else if strings.HasPrefix(name, "personal_user_") {
			name = "Personal workspace"
		}
		if runes := []rune(name); len(runes) > 80 {
			name = string(runes[:80])
		}
		var id string
		if err = tx.QueryRowContext(ctx, `INSERT INTO workspaces(owner_user_id,name) VALUES($1,$2) RETURNING id::text`, item.OwnerUserID, name).Scan(&id); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO workspace_id_mapping VALUES($1,$2,$3)`, item.LegacyID, id, item.OwnerUserID); err != nil {
			return err
		}
	}
	// Remove only explicitly superseded cross-owner grants. An absent grant stays
	// absent; preserving onboarding markers prevents login from regranting access.
	if _, err = tx.ExecContext(ctx, `DELETE FROM auth_memberships m USING workspace_id_mapping x WHERE m.workspace_id::text=x.legacy_id AND m.user_id<>x.owner_user_id`); err != nil {
		return err
	}
	for _, table := range tables {
		if len(plan) == 0 {
			break
		}
		if _, err = tx.ExecContext(ctx, `UPDATE `+table+` t SET workspace_id=x.workspace_id FROM workspace_id_mapping x WHERE t.workspace_id=x.legacy_id`); err != nil {
			return err
		}
	}
	// An explicit reassignment grants the chosen owner access. An inferred owner
	// keeps its existing authorization, including a deliberately revoked grant.
	for _, item := range plan {
		if owners[item.LegacyID] != "" && !slices.Contains(item.KnownOwners, item.OwnerUserID) {
			if _, err = tx.ExecContext(ctx, `INSERT INTO auth_memberships(user_id,workspace_id) SELECT owner_user_id,workspace_id FROM workspace_id_mapping WHERE legacy_id=$1 ON CONFLICT DO NOTHING`, item.LegacyID); err != nil {
				return err
			}
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE task_state_revisions s SET state=jsonb_set(s.state,'{workspace_id}',to_jsonb(s.workspace_id::text),true) FROM workspace_id_mapping x WHERE s.workspace_id::text=x.workspace_id`); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE auth_personal_workspaces p SET workspace_id=NULL WHERE workspace_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM workspaces w WHERE w.id::text=p.workspace_id::text AND w.owner_user_id=p.user_id)`); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO auth_personal_workspaces(user_id,workspace_id) SELECT m.user_id,MIN(m.workspace_id) FROM auth_memberships m JOIN workspaces w ON w.id::text=m.workspace_id::text AND w.owner_user_id=m.user_id GROUP BY m.user_id ON CONFLICT(user_id) DO NOTHING`); err != nil {
		return err
	}
	if err = upgradeWorkspacePrimaryKey(ctx, tx, tables); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO workspace_migrations(legacy_id,workspace_id,owner_user_id) SELECT legacy_id,workspace_id::bigint,owner_user_id FROM workspace_id_mapping`); err != nil {
		return err
	}
	for _, table := range tables {
		if _, err = tx.ExecContext(ctx, `ALTER TABLE `+table+` ALTER COLUMN workspace_id DROP DEFAULT`); err != nil {
			return err
		}
		constraint := table + "_workspace_entity_fk"
		var exists bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_constraint WHERE conname=$1 AND conrelid=$2::regclass)`, constraint, table).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			fk := `FOREIGN KEY(workspace_id) REFERENCES workspaces(id)`
			if table == "auth_memberships" || table == "auth_personal_workspaces" {
				fk = `FOREIGN KEY(user_id,workspace_id) REFERENCES workspaces(owner_user_id,id)`
			}
			if _, err = tx.ExecContext(ctx, `ALTER TABLE `+table+` ADD CONSTRAINT `+constraint+` `+fk); err != nil {
				return err
			}
		}
	}
	if err = installWorkspaceWriteGuards(ctx, tx, tables); err != nil {
		return err
	}
	return tx.Commit()
}

func installWorkspaceWriteGuards(ctx context.Context, tx *sql.Tx, tables []string) error {
	// Each business write locks the entity for SHARE. Lifecycle UPDATE obtains
	// the conflicting row lock, so admission cannot race the closing decision.
	_, err := tx.ExecContext(ctx, `CREATE OR REPLACE FUNCTION require_active_workspace() RETURNS trigger LANGUAGE plpgsql AS $$
	 DECLARE body jsonb; scope text;
	 BEGIN
	 IF TG_OP='DELETE' THEN body=to_jsonb(OLD); ELSE body=to_jsonb(NEW); END IF;
	 -- Retention is deletion of expired sensitive payloads, not a business write.
	 -- Permit only exact erasure; identities, metadata and expiry cannot change.
	 IF TG_OP='UPDATE' AND TG_TABLE_NAME='model_request_records' THEN
	   IF (to_jsonb(OLD)->>'capture_expires_at')::timestamptz<=NOW()
	     AND body->>'capture_content'='' AND body->>'capture_content_hash'=''
	     AND body->>'capture_stored_bytes'='0' AND body->>'capture_reconstructable'='false' AND body->>'capture_expired'='true'
	     AND body-ARRAY['capture_content','capture_content_hash','capture_stored_bytes','capture_reconstructable','capture_expired']
	       =to_jsonb(OLD)-ARRAY['capture_content','capture_content_hash','capture_stored_bytes','capture_reconstructable','capture_expired'] THEN RETURN NEW; END IF;
	 ELSIF TG_OP='UPDATE' AND TG_TABLE_NAME='tool_artifacts' THEN
	   IF (to_jsonb(OLD)->>'expires_at')::timestamptz<=NOW() AND body->>'content'='\x'
	     AND body-'content'=to_jsonb(OLD)-'content' THEN RETURN NEW; END IF;
	 END IF;
	 scope=body->>'workspace_id';
	 IF scope IS NULL AND body->>'run_id' IS NOT NULL THEN SELECT workspace_id INTO scope FROM runs WHERE id=body->>'run_id'; END IF;
	 IF scope IS NOT NULL THEN
	   PERFORM 1 FROM workspaces WHERE id=scope::bigint AND status='active' AND deleted_at IS NULL FOR SHARE;
	   IF NOT FOUND THEN RAISE EXCEPTION 'Workspace is archived or deleted; writes and execution are disabled' USING ERRCODE='PWS01'; END IF;
	 END IF;
	 IF TG_OP='DELETE' THEN RETURN OLD; ELSE RETURN NEW; END IF;
	 END $$`)
	if err != nil {
		return err
	}
	guarded := append(slices.Clone(tables), "tool_effects", "tool_artifacts", "stage_checkpoints", "context_compactions", "verification_evidence", "verification_artifacts", "run_usage_entries", "run_events", "model_request_records")
	for _, table := range guarded {
		if table == "auth_memberships" || table == "auth_personal_workspaces" {
			continue
		}
		if _, err = tx.ExecContext(ctx, `CREATE OR REPLACE TRIGGER workspace_active_write BEFORE INSERT OR UPDATE OR DELETE ON `+table+` FOR EACH ROW EXECUTE FUNCTION require_active_workspace()`); err != nil {
			return err
		}
	}
	return nil
}
