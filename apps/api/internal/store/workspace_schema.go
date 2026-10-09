package store

import (
	"context"
	"database/sql"
	"errors"
	"slices"

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

// Scoped tables reference the Workspace primary key. Run-owned records inherit
// scope through their Run foreign key.
var workspaceReferenceTables = []string{"agents", "workspace_tool_config", "conversations", "messages", "runs", "collaboration_steps", "memories", "memory_changes", "memory_candidates", "documents", "task_state_revisions", "auth_memberships", "auth_personal_workspaces"}

func (s *PostgresStore) InitializeWorkspaceLifecycle(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, table := range workspaceReferenceTables {
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
			if _, err = tx.ExecContext(ctx, "ALTER TABLE "+table+" ADD CONSTRAINT "+constraint+" "+fk); err != nil {
				return err
			}
		}
	}
	if err = installWorkspaceWriteGuards(ctx, tx, workspaceReferenceTables); err != nil {
		return err
	}
	// Never overwrite an OIDC account that collides with the reserved local ID.
	result, err := tx.ExecContext(ctx, `INSERT INTO auth_users(id,issuer,subject,name) VALUES($1,'agentflow:local','local','Super')
 ON CONFLICT(id) DO UPDATE SET name=EXCLUDED.name WHERE auth_users.issuer=EXCLUDED.issuer AND auth_users.subject=EXCLUDED.subject`, identity.SuperUserID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return errors.New("reserved local identity is owned by another issuer or subject")
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	return s.ProvisionPersonalWorkspace(ctx, identity.SuperUserID)
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
	guarded := append(slices.Clone(tables), "conversation_inputs", "tool_effects", "tool_artifacts", "stage_checkpoints", "context_compactions", "verification_evidence", "verification_artifacts", "run_usage_entries", "run_events", "model_request_records")
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
