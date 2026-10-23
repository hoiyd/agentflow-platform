package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"agentflow-platform/apps/api/internal/domain"
)

const mcpConnectionColumns = `id::text,workspace_id::text,name,url,auth_mode,enabled,read_only_tools,revision,status,credential_ciphertext,discovery,created_at,updated_at`

func scanMCPConnection(row scanner) (domain.MCPConnection, error) {
	var c domain.MCPConnection
	var selected, discovery []byte
	err := row.Scan(&c.ID, &c.WorkspaceID, &c.Name, &c.URL, &c.AuthMode, &c.Enabled, &selected, &c.Revision, &c.Status, &c.CredentialCiphertext, &discovery, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return c, ErrNotFound("MCP connection")
	}
	if err != nil {
		return c, err
	}
	if err = json.Unmarshal(selected, &c.ReadOnlyTools); err != nil {
		return c, err
	}
	err = json.Unmarshal(discovery, &c.Discovery)
	return c, err
}

func (s *PostgresStore) ListMCPConnections(ctx context.Context, workspace string) ([]domain.MCPConnection, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+mcpConnectionColumns+` FROM mcp_connections WHERE workspace_id=$1 AND deleted_at IS NULL ORDER BY id`, workspace)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []domain.MCPConnection{}
	for rows.Next() {
		c, err := scanMCPConnection(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, c)
	}
	return items, rows.Err()
}

func (s *PostgresStore) GetMCPConnection(ctx context.Context, workspace, id string) (domain.MCPConnection, error) {
	return scanMCPConnection(s.db.QueryRowContext(ctx, `SELECT `+mcpConnectionColumns+` FROM mcp_connections WHERE workspace_id=$1 AND id=$2 AND deleted_at IS NULL`, workspace, id))
}

func (s *PostgresStore) SaveMCPConnection(ctx context.Context, c domain.MCPConnection) (domain.MCPConnection, error) {
	selected, err := json.Marshal(c.ReadOnlyTools)
	if err != nil {
		return c, err
	}
	discovery, err := json.Marshal(c.Discovery)
	if err != nil {
		return c, err
	}
	if c.ID == "" {
		// Serialize the per-Workspace cap with lifecycle changes and other inserts.
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return c, err
		}
		defer tx.Rollback()
		var active bool
		if err = tx.QueryRowContext(ctx, `SELECT status='active' AND deleted_at IS NULL FROM workspaces WHERE id=$1 FOR UPDATE`, c.WorkspaceID).Scan(&active); err != nil {
			return c, err
		}
		if !active {
			return c, &WorkspaceError{Message: "Workspace is inactive", Conflict: true}
		}
		var count int
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM mcp_connections WHERE workspace_id=$1 AND deleted_at IS NULL`, c.WorkspaceID).Scan(&count); err != nil {
			return c, err
		}
		if count >= 8 {
			return c, &WorkspaceError{Message: "Workspace supports at most 8 MCP connections", Conflict: true}
		}
		created, err := scanMCPConnection(tx.QueryRowContext(ctx, `INSERT INTO mcp_connections(workspace_id,name,url,auth_mode,enabled,read_only_tools,status,credential_ciphertext,discovery) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING `+mcpConnectionColumns, c.WorkspaceID, c.Name, c.URL, c.AuthMode, c.Enabled, selected, c.Status, c.CredentialCiphertext, discovery))
		if err != nil {
			return c, err
		}
		return created, tx.Commit()
	}
	updated, err := scanMCPConnection(s.db.QueryRowContext(ctx, `UPDATE mcp_connections SET name=$3,url=$4,auth_mode=$5,enabled=$6,read_only_tools=$7,status=$8,credential_ciphertext=$9,discovery=$10,revision=revision+1,updated_at=NOW() WHERE workspace_id=$1 AND id=$2 AND revision=$11 AND deleted_at IS NULL RETURNING `+mcpConnectionColumns, c.WorkspaceID, c.ID, c.Name, c.URL, c.AuthMode, c.Enabled, selected, c.Status, c.CredentialCiphertext, discovery, c.Revision))
	if IsNotFound(err) {
		if _, getErr := s.GetMCPConnection(ctx, c.WorkspaceID, c.ID); getErr != nil {
			return c, getErr
		}
		return c, &WorkspaceError{Message: "MCP connection changed; reload before saving", Conflict: true}
	}
	return updated, err
}

func (s *PostgresStore) DeleteMCPConnection(ctx context.Context, workspace, id string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE mcp_connections SET deleted_at=NOW(),enabled=false,credential_ciphertext='',discovery='[]',revision=revision+1,updated_at=NOW() WHERE workspace_id=$1 AND id=$2 AND deleted_at IS NULL`, workspace, id)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err == nil && count == 0 {
		return ErrNotFound("MCP connection")
	}
	return err
}
