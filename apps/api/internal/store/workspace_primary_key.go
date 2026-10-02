package store

import (
	"context"
	"database/sql"
)

// Called inside the offline migration transaction after namespace mappings are
// resolved. Drop only Workspace-reference FKs; never use DROP ... CASCADE.
func upgradeWorkspacePrimaryKey(ctx context.Context, tx *sql.Tx, tables []string) error {
	if _, err := tx.ExecContext(ctx, `DO $$ DECLARE fk record; BEGIN
	 FOR fk IN SELECT conrelid::regclass AS relation, conname FROM pg_constraint
	   WHERE contype='f' AND confrelid='workspaces'::regclass
	   AND EXISTS(SELECT 1 FROM pg_attribute a WHERE a.attrelid=conrelid AND a.attnum=ANY(conkey) AND a.attname='workspace_id' AND a.atttypid='text'::regtype)
	 LOOP EXECUTE format('ALTER TABLE %s DROP CONSTRAINT %I',fk.relation,fk.conname); END LOOP;
	 END $$`); err != nil {
		return err
	}
	for _, table := range append(append([]string{}, tables...), "workspace_migrations") {
		var kind string
		if err := tx.QueryRowContext(ctx, `SELECT udt_name FROM information_schema.columns WHERE table_schema=current_schema() AND table_name=$1 AND column_name='workspace_id'`, table).Scan(&kind); err != nil {
			return err
		}
		if kind == "int8" {
			continue
		}
		// The previous triggers target the generated alias and must be replaced
		// before any row writes under the new types/identity.
		if _, err := tx.ExecContext(ctx, `DROP TRIGGER IF EXISTS workspace_active_write ON `+table+`; ALTER TABLE `+table+` ALTER COLUMN workspace_id DROP DEFAULT; ALTER TABLE `+table+` ALTER COLUMN workspace_id TYPE bigint USING workspace_id::bigint`); err != nil {
			return err
		}
	}
	_, err := tx.ExecContext(ctx, `ALTER TABLE workspaces DROP COLUMN IF EXISTS workspace_id;
	 DO $$ BEGIN
	 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='workspaces'::regclass AND conname='workspaces_owner_user_id_id_key') THEN
	   ALTER TABLE workspaces ADD CONSTRAINT workspaces_owner_user_id_id_key UNIQUE(owner_user_id,id);
	 END IF;
	 END $$;
	 DO $$ BEGIN
	 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='workspace_migrations'::regclass AND contype='f' AND confrelid='workspaces'::regclass) THEN
	   ALTER TABLE workspace_migrations ADD CONSTRAINT workspace_migrations_workspace_id_fkey FOREIGN KEY(workspace_id) REFERENCES workspaces(id);
	 END IF;
	 END $$`)
	return err
}
