package store

import (
	"database/sql"
	"errors"

	"agentflow-platform/apps/api/internal/domain"
)

func (s *PostgresStore) ListAgentsByWorkspace(workspaceID string) ([]domain.Agent, error) {
	rows, err := s.db.Query(`SELECT id,COALESCE(workspace_id::text,''),is_template,name,description,system_prompt,routing_hints,tools,skills,memory_enabled,retrieval_enabled,executor,deleted_at,created_at,updated_at
 FROM agents WHERE deleted_at IS NULL AND (workspace_id=$1 OR is_template) ORDER BY is_template,created_at,id`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []domain.Agent{}
	for rows.Next() {
		item, err := scanAgent(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
func (s *PostgresStore) GetAgentInWorkspace(workspaceID, id string) (domain.Agent, bool, error) {
	row := s.db.QueryRow(`SELECT id,COALESCE(workspace_id::text,''),is_template,name,description,system_prompt,routing_hints,tools,skills,memory_enabled,retrieval_enabled,executor,deleted_at,created_at,updated_at
 FROM agents WHERE id=$2 AND (workspace_id=$1 OR is_template)`, workspaceID, id)
	item, err := scanAgent(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Agent{}, false, nil
	}
	return item, err == nil, err
}
