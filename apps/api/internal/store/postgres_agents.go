package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/domain"
)

func (s *PostgresStore) ListAgents() ([]domain.Agent, error) {
	rows, err := s.db.Query(`
		SELECT id, COALESCE(workspace_id::text,''), is_template, name, description, system_prompt, routing_hints, tools, skills, memory_enabled, retrieval_enabled, executor, deleted_at, created_at, updated_at
		FROM agents
		WHERE deleted_at IS NULL
		ORDER BY created_at ASC`)
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

func (s *PostgresStore) CreateAgent(agent domain.Agent) (domain.Agent, error) {
	now := time.Now().UTC()
	agent.ID = strings.TrimSpace(agent.ID)
	if agent.ID == "" {
		agent.ID = NewID("agent")
	}
	agent.Name = strings.TrimSpace(agent.Name)
	if agent.Name == "" {
		return domain.Agent{}, errors.New("agent name is required")
	}
	agent.Description = strings.TrimSpace(agent.Description)
	agent.SystemPrompt = strings.TrimSpace(agent.SystemPrompt)
	agent.Tools = NormalizeTools(agent.Tools)
	agent = domain.NormalizeAgentConfig(agent)
	agent.CreatedAt = now
	agent.UpdatedAt = now

	routingHintsJSON, err := json.Marshal(agent.RoutingHints)
	if err != nil {
		return domain.Agent{}, err
	}
	toolsJSON, err := json.Marshal(agent.Tools)
	if err != nil {
		return domain.Agent{}, err
	}
	skillsJSON, err := json.Marshal(agent.Skills)
	if err != nil {
		return domain.Agent{}, err
	}
	_, err = s.db.Exec(`
		INSERT INTO agents (id, name, description, system_prompt, routing_hints, tools, skills, memory_enabled, retrieval_enabled, executor, deleted_at, created_at, updated_at, workspace_id, is_template)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, NULL, $11, $12, $13, $14)`,
		agent.ID, agent.Name, agent.Description, agent.SystemPrompt, routingHintsJSON, toolsJSON, skillsJSON, agent.MemoryEnabled, agent.RetrievalEnabled, agent.Executor, agent.CreatedAt, agent.UpdatedAt, nullString(agent.WorkspaceID), agent.IsTemplate)
	return agent, err
}

func (s *PostgresStore) GetAgent(id string) (domain.Agent, bool, error) {
	row := s.db.QueryRow(`
		SELECT id, COALESCE(workspace_id::text,''), is_template, name, description, system_prompt, routing_hints, tools, skills, memory_enabled, retrieval_enabled, executor, deleted_at, created_at, updated_at
		FROM agents
		WHERE id = $1`, id)
	agent, err := scanAgent(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Agent{}, false, nil
	}
	if err != nil {
		return domain.Agent{}, false, err
	}
	return agent, true, nil
}

func (s *PostgresStore) UpdateAgent(agent domain.Agent) (domain.Agent, error) {
	existing, ok, err := s.GetAgent(agent.ID)
	if err != nil {
		return domain.Agent{}, err
	}
	if !ok {
		return domain.Agent{}, errors.New("agent not found")
	}
	if existing.IsTemplate {
		return domain.Agent{}, errors.New("built-in templates are read-only; create a Workspace copy")
	}
	agent.WorkspaceID = existing.WorkspaceID
	agent.IsTemplate = false

	agent.Name = strings.TrimSpace(agent.Name)
	if agent.Name == "" {
		return domain.Agent{}, errors.New("agent name is required")
	}
	agent.Description = strings.TrimSpace(agent.Description)
	agent.SystemPrompt = strings.TrimSpace(agent.SystemPrompt)
	agent.Tools = NormalizeTools(agent.Tools)
	agent = domain.NormalizeAgentConfig(agent)
	agent.Archived = existing.Archived
	agent.CreatedAt = existing.CreatedAt
	agent.UpdatedAt = time.Now().UTC()
	routingHintsJSON, err := json.Marshal(agent.RoutingHints)
	if err != nil {
		return domain.Agent{}, err
	}
	toolsJSON, err := json.Marshal(agent.Tools)
	if err != nil {
		return domain.Agent{}, err
	}
	skillsJSON, err := json.Marshal(agent.Skills)
	if err != nil {
		return domain.Agent{}, err
	}
	_, err = s.db.Exec(`
		UPDATE agents
		SET name = $1, description = $2, system_prompt = $3, routing_hints = $4, tools = $5, skills = $6, memory_enabled = $7, retrieval_enabled = $8, executor = $9, updated_at = $10
		WHERE id = $11`,
		agent.Name, agent.Description, agent.SystemPrompt, routingHintsJSON, toolsJSON, skillsJSON, agent.MemoryEnabled, agent.RetrievalEnabled, agent.Executor, agent.UpdatedAt, agent.ID)
	return agent, err
}

func (s *PostgresStore) ArchiveAgent(id string) error {
	id = strings.TrimSpace(id)
	agent, ok, err := s.GetAgent(id)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotFound("agent")
	}
	if agent.IsTemplate {
		return errors.New("default agents cannot be archived")
	}
	now := time.Now().UTC()
	result, err := s.db.Exec(`
		UPDATE agents
		SET deleted_at = $1, updated_at = $1
		WHERE id = $2`,
		now, id)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return errors.New("agent not found")
	}
	return nil
}

func (s *PostgresStore) GetDefaultAgent() (domain.Agent, bool, error) {
	if agent, ok, err := s.GetAgent("agent_planner"); err != nil || (ok && !agent.Archived) {
		return agent, ok, err
	}
	row := s.db.QueryRow(`
		SELECT id, COALESCE(workspace_id::text,''), is_template, name, description, system_prompt, routing_hints, tools, skills, memory_enabled, retrieval_enabled, executor, deleted_at, created_at, updated_at
		FROM agents
		WHERE deleted_at IS NULL
		ORDER BY created_at ASC
		LIMIT 1`)
	agent, err := scanAgent(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Agent{}, false, nil
	}
	if err != nil {
		return domain.Agent{}, false, err
	}
	return agent, true, nil
}

func (s *PostgresStore) seedDefaultAgents(ctx context.Context) error {
	for _, agent := range DefaultAgents(time.Now().UTC()) {
		existing, ok, err := s.GetAgent(agent.ID)
		if err != nil {
			return err
		}
		// Existing global profiles may contain private edits. Never turn them into
		// shared templates or overwrite them while bootstrapping the new schema.
		if ok && !existing.IsTemplate {
			agent.ID = "template_" + strings.TrimPrefix(agent.ID, "agent_")
		}
		hints, _ := json.Marshal(agent.RoutingHints)
		tools, _ := json.Marshal(agent.Tools)
		_, err = s.db.ExecContext(ctx, `INSERT INTO agents(id,name,description,system_prompt,routing_hints,tools,skills,memory_enabled,retrieval_enabled,executor,is_template,created_at,updated_at)
  VALUES($1,$2,$3,$4,$5,$6,'[]', $7,$8,$9,true,$10,$10) ON CONFLICT(id) DO NOTHING`, agent.ID, agent.Name, agent.Description, agent.SystemPrompt, hints, tools, agent.MemoryEnabled, agent.RetrievalEnabled, agent.Executor, agent.CreatedAt)
		if err != nil {
			return err
		}
	}
	return nil
}

func scanAgent(row scanner) (domain.Agent, error) {
	var agent domain.Agent
	var routingHintsJSON []byte
	var toolsJSON []byte
	var skillsJSON []byte
	var deletedAt sql.NullTime
	if err := row.Scan(&agent.ID, &agent.WorkspaceID, &agent.IsTemplate, &agent.Name, &agent.Description, &agent.SystemPrompt, &routingHintsJSON, &toolsJSON, &skillsJSON, &agent.MemoryEnabled, &agent.RetrievalEnabled, &agent.Executor, &deletedAt, &agent.CreatedAt, &agent.UpdatedAt); err != nil {
		return domain.Agent{}, err
	}
	agent.Archived = deletedAt.Valid
	if len(routingHintsJSON) > 0 {
		if err := json.Unmarshal(routingHintsJSON, &agent.RoutingHints); err != nil {
			return domain.Agent{}, err
		}
	}
	if len(toolsJSON) > 0 {
		if err := json.Unmarshal(toolsJSON, &agent.Tools); err != nil {
			return domain.Agent{}, err
		}
	}
	if len(skillsJSON) > 0 {
		if err := json.Unmarshal(skillsJSON, &agent.Skills); err != nil {
			return domain.Agent{}, err
		}
	}
	return domain.NormalizeAgentConfig(agent), nil
}
