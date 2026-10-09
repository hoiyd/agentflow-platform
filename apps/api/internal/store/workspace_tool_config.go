package store

import (
	"database/sql"
	"encoding/json"
	"errors"

	"agentflow-platform/apps/api/internal/domain"
)

type WorkspaceToolStore interface {
	CheckWorkspaceExecution(workspaceID string) error
	EnsureWorkspaceToolConfig(workspaceID string, initial []string) (domain.WorkspaceToolConfig, error)
	SetWorkspaceToolEnabled(workspaceID, name string, enabled bool) error
}

func (s *PostgresStore) EnsureWorkspaceToolConfig(workspaceID string, initial []string) (domain.WorkspaceToolConfig, error) {
	var config domain.WorkspaceToolConfig
	var raw []byte
	err := s.db.QueryRow(`SELECT workspace_id::text,allowed_tools,revision FROM workspace_tool_config WHERE workspace_id=$1`, workspaceID).Scan(&config.WorkspaceID, &raw, &config.Revision)
	if err == nil {
		err = json.Unmarshal(raw, &config.AllowedTools)
		return config, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return config, err
	}
	var active bool
	err = s.db.QueryRow(`SELECT status='active' FROM workspaces WHERE id=$1 AND deleted_at IS NULL`, workspaceID).Scan(&active)
	if errors.Is(err, sql.ErrNoRows) {
		return config, ErrNotFound("workspace")
	}
	if err != nil {
		return config, err
	}
	// Reading archived configuration must not create new grants or trigger a write.
	if !active {
		return domain.WorkspaceToolConfig{WorkspaceID: workspaceID, AllowedTools: []string{}}, nil
	}
	// DO NOTHING makes initialization stable across requests and processes. An
	// empty persisted list is never mistaken for an uninitialized configuration.
	encoded, err := json.Marshal(NormalizeTools(initial))
	if err != nil {
		return domain.WorkspaceToolConfig{}, err
	}
	_, err = s.db.Exec(`INSERT INTO workspace_tool_config(workspace_id,allowed_tools) VALUES($1,$2) ON CONFLICT(workspace_id) DO NOTHING`, workspaceID, encoded)
	if err != nil {
		return domain.WorkspaceToolConfig{}, err
	}
	err = s.db.QueryRow(`SELECT workspace_id::text,allowed_tools,revision FROM workspace_tool_config WHERE workspace_id=$1`, workspaceID).Scan(&config.WorkspaceID, &raw, &config.Revision)
	if err != nil {
		return config, err
	}
	err = json.Unmarshal(raw, &config.AllowedTools)
	return config, err
}

func (s *PostgresStore) CheckWorkspaceExecution(workspaceID string) error {
	var active bool
	err := s.db.QueryRow(`SELECT status='active' AND deleted_at IS NULL AND EXISTS(SELECT 1 FROM auth_memberships m WHERE m.workspace_id=w.id AND m.user_id=w.owner_user_id) FROM workspaces w WHERE id=$1`, workspaceID).Scan(&active)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound("workspace")
	}
	if err != nil {
		return err
	}
	if !active {
		return &WorkspaceError{Message: "Workspace is inactive or owner Membership was revoked; execution is disabled", Conflict: true}
	}
	return nil
}

func (s *PostgresStore) SetWorkspaceToolEnabled(workspaceID, name string, enabled bool) error {
	result, err := s.db.Exec(`UPDATE workspace_tool_config SET allowed_tools=CASE WHEN $3 THEN
 CASE WHEN allowed_tools ? $2 THEN allowed_tools ELSE allowed_tools || jsonb_build_array($2::text) END
 ELSE allowed_tools - $2::text END,revision=revision+1,updated_at=NOW() WHERE workspace_id=$1`, workspaceID, name, enabled)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n == 0 {
		return ErrNotFound("workspace tool config")
	}
	return err
}
