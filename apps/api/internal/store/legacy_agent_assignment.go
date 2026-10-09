package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

type LegacyAgentAssignment struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	WorkspaceID     string `json:"workspace_id"`
	CopyWorkspaceID string `json:"copy_workspace_id,omitempty"`
	CopyID          string `json:"copy_id,omitempty"`
}

// AssignLegacyAgents is an explicit operator migration, never a startup owner
// guess. One transaction retains original IDs and makes independent copies.
// A second application finds no unassigned records and is a no-op.
func (s *PostgresStore) AssignLegacyAgents(ctx context.Context, workspaceID, copyWorkspaceID string, apply bool) ([]LegacyAgentAssignment, error) {
	if workspaceID == "" || workspaceID == copyWorkspaceID {
		return nil, errors.New("distinct target Workspace IDs are required")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `LOCK TABLE agents IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		return nil, err
	}
	for _, id := range []string{workspaceID, copyWorkspaceID} {
		if id == "" {
			continue
		}
		var active bool
		err = tx.QueryRowContext(ctx, `SELECT status='active' AND deleted_at IS NULL FROM workspaces WHERE id=$1 FOR SHARE`, id).Scan(&active)
		if err != nil {
			return nil, err
		}
		if !active {
			return nil, fmt.Errorf("target Workspace %s is not active", id)
		}
	}
	var busy bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM runs WHERE status IN('running','queued'))`).Scan(&busy); err != nil {
		return nil, err
	}
	if busy {
		return nil, errors.New("stop active Run execution before migrating Agent ownership")
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,name FROM agents WHERE workspace_id IS NULL AND NOT is_template ORDER BY id`)
	if err != nil {
		return nil, err
	}
	items := []LegacyAgentAssignment{}
	for rows.Next() {
		var item LegacyAgentAssignment
		if err = rows.Scan(&item.ID, &item.Name); err != nil {
			rows.Close()
			return nil, err
		}
		item.WorkspaceID = workspaceID
		item.CopyWorkspaceID = copyWorkspaceID
		items = append(items, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if !apply {
		return items, nil
	}
	for i := range items {
		item := &items[i]
		if copyWorkspaceID != "" {
			item.CopyID = NewID("agent")
			_, err = tx.ExecContext(ctx, `INSERT INTO agents(id,workspace_id,is_template,name,description,system_prompt,routing_hints,tools,skills,memory_enabled,retrieval_enabled,executor,deleted_at,created_at,updated_at)
   SELECT $2,$3,false,name,description,system_prompt,routing_hints,tools,skills,memory_enabled,retrieval_enabled,executor,deleted_at,created_at,updated_at FROM agents WHERE id=$1`, item.ID, item.CopyID, copyWorkspaceID)
			if err != nil {
				return nil, err
			}
		}
		if _, err = tx.ExecContext(ctx, `UPDATE agents SET workspace_id=$2 WHERE id=$1 AND workspace_id IS NULL AND NOT is_template`, item.ID, workspaceID); err != nil {
			return nil, err
		}
	}
	return items, tx.Commit()
}
