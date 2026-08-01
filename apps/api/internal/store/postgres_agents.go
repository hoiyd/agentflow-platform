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
		SELECT id, name, description, system_prompt, tools, memory_enabled, retrieval_enabled, executor, deleted_at, created_at, updated_at
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
		agent.ID = newID("agent")
	}
	agent.Name = strings.TrimSpace(agent.Name)
	if agent.Name == "" {
		return domain.Agent{}, errors.New("agent name is required")
	}
	agent.Description = strings.TrimSpace(agent.Description)
	agent.SystemPrompt = strings.TrimSpace(agent.SystemPrompt)
	agent.Tools = normalizeTools(agent.Tools)
	agent = domain.NormalizeAgentConfig(agent)
	agent.CreatedAt = now
	agent.UpdatedAt = now

	toolsJSON, err := json.Marshal(agent.Tools)
	if err != nil {
		return domain.Agent{}, err
	}
	_, err = s.db.Exec(`
		INSERT INTO agents (id, name, description, system_prompt, tools, memory_enabled, retrieval_enabled, executor, deleted_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NULL, $9, $10)`,
		agent.ID, agent.Name, agent.Description, agent.SystemPrompt, toolsJSON, agent.MemoryEnabled, agent.RetrievalEnabled, agent.Executor, agent.CreatedAt, agent.UpdatedAt)
	return agent, err
}

func (s *PostgresStore) GetAgent(id string) (domain.Agent, bool, error) {
	row := s.db.QueryRow(`
		SELECT id, name, description, system_prompt, tools, memory_enabled, retrieval_enabled, executor, deleted_at, created_at, updated_at
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

	agent.Name = strings.TrimSpace(agent.Name)
	if agent.Name == "" {
		return domain.Agent{}, errors.New("agent name is required")
	}
	agent.Description = strings.TrimSpace(agent.Description)
	agent.SystemPrompt = strings.TrimSpace(agent.SystemPrompt)
	agent.Tools = normalizeTools(agent.Tools)
	agent = domain.NormalizeAgentConfig(agent)
	agent.Archived = existing.Archived
	agent.CreatedAt = existing.CreatedAt
	agent.UpdatedAt = time.Now().UTC()
	toolsJSON, err := json.Marshal(agent.Tools)
	if err != nil {
		return domain.Agent{}, err
	}
	_, err = s.db.Exec(`
		UPDATE agents
		SET name = $1, description = $2, system_prompt = $3, tools = $4, memory_enabled = $5, retrieval_enabled = $6, executor = $7, updated_at = $8
		WHERE id = $9`,
		agent.Name, agent.Description, agent.SystemPrompt, toolsJSON, agent.MemoryEnabled, agent.RetrievalEnabled, agent.Executor, agent.UpdatedAt, agent.ID)
	return agent, err
}

func (s *PostgresStore) ArchiveAgent(id string) error {
	id = strings.TrimSpace(id)
	if domain.IsDefaultAgentID(id) {
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
		SELECT id, name, description, system_prompt, tools, memory_enabled, retrieval_enabled, executor, deleted_at, created_at, updated_at
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
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM agents`).Scan(&count); err != nil {
		return err
	}
	now := time.Now().UTC()
	if count == 0 {
		for _, agent := range defaultAgents(now) {
			toolsJSON, err := json.Marshal(agent.Tools)
			if err != nil {
				return err
			}
			if _, err := s.db.ExecContext(ctx, `
				INSERT INTO agents (id, name, description, system_prompt, tools, memory_enabled, retrieval_enabled, executor, deleted_at, created_at, updated_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NULL, $9, $10)`,
				agent.ID, agent.Name, agent.Description, agent.SystemPrompt, toolsJSON, agent.MemoryEnabled, agent.RetrievalEnabled, agent.Executor, agent.CreatedAt, agent.UpdatedAt); err != nil {
				return err
			}
		}
		return nil
	}

	agents, err := s.ListAgents()
	if err != nil {
		return err
	}
	for _, agent := range agents {
		next := agent
		if !updateDefaultAgentText(&next, defaultAgentByID(agent.ID)) {
			continue
		}
		next.UpdatedAt = now
		if _, err := s.UpdateAgent(next); err != nil {
			return err
		}
	}
	return nil
}

func scanAgent(row scanner) (domain.Agent, error) {
	var agent domain.Agent
	var toolsJSON []byte
	var deletedAt sql.NullTime
	if err := row.Scan(&agent.ID, &agent.Name, &agent.Description, &agent.SystemPrompt, &toolsJSON, &agent.MemoryEnabled, &agent.RetrievalEnabled, &agent.Executor, &deletedAt, &agent.CreatedAt, &agent.UpdatedAt); err != nil {
		return domain.Agent{}, err
	}
	agent.Archived = deletedAt.Valid
	if len(toolsJSON) > 0 {
		if err := json.Unmarshal(toolsJSON, &agent.Tools); err != nil {
			return domain.Agent{}, err
		}
	}
	return domain.NormalizeAgentConfig(agent), nil
}

func defaultAgentByID(id string) domain.Agent {
	for _, agent := range defaultAgents(time.Now().UTC()) {
		if agent.ID == id {
			return agent
		}
	}
	return domain.Agent{}
}
