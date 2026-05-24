package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/domain"

	_ "github.com/jackc/pgx/v5/stdlib"
)

type PostgresStore struct {
	db *sql.DB
}

func NewPostgresStore(databaseURL string) (*PostgresStore, error) {
	if strings.TrimSpace(databaseURL) == "" {
		return nil, errors.New("DATABASE_URL is required when STORE_DRIVER=postgres")
	}
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(30 * time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}

	store := &PostgresStore{db: db}
	if err := store.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := store.seedDefaultAgents(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *PostgresStore) Close() error {
	return s.db.Close()
}

func (s *PostgresStore) ListConversations() ([]domain.Conversation, error) {
	rows, err := s.db.Query(`
		SELECT id, title, created_at, updated_at
		FROM conversations
		ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []domain.Conversation{}
	for rows.Next() {
		var item domain.Conversation
		if err := rows.Scan(&item.ID, &item.Title, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *PostgresStore) CreateConversation(title string) (domain.Conversation, error) {
	now := time.Now().UTC()
	conversation := domain.Conversation{
		ID:        newID("conv"),
		Title:     normalizeTitle(title),
		CreatedAt: now,
		UpdatedAt: now,
	}
	_, err := s.db.Exec(`
		INSERT INTO conversations (id, title, created_at, updated_at)
		VALUES ($1, $2, $3, $4)`,
		conversation.ID, conversation.Title, conversation.CreatedAt, conversation.UpdatedAt)
	return conversation, err
}

func (s *PostgresStore) GetConversation(id string) (domain.Conversation, bool, error) {
	var item domain.Conversation
	err := s.db.QueryRow(`
		SELECT id, title, created_at, updated_at
		FROM conversations
		WHERE id = $1`, id).Scan(&item.ID, &item.Title, &item.CreatedAt, &item.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Conversation{}, false, nil
	}
	if err != nil {
		return domain.Conversation{}, false, err
	}
	return item, true, nil
}

func (s *PostgresStore) DeleteConversation(id string) error {
	result, err := s.db.Exec(`DELETE FROM conversations WHERE id = $1`, id)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err == nil && affected == 0 {
		return ErrNotFound("conversation")
	}
	return nil
}

func (s *PostgresStore) ListMessages(conversationID string) ([]domain.Message, error) {
	rows, err := s.db.Query(`
		SELECT id, conversation_id, role, content, created_at
		FROM messages
		WHERE conversation_id = $1
		ORDER BY created_at ASC`, conversationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []domain.Message{}
	for rows.Next() {
		var item domain.Message
		if err := rows.Scan(&item.ID, &item.ConversationID, &item.Role, &item.Content, &item.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *PostgresStore) AddMessage(conversationID string, role string, content string) (domain.Message, error) {
	now := time.Now().UTC()
	message := domain.Message{
		ID:             newID("msg"),
		ConversationID: conversationID,
		Role:           role,
		Content:        content,
		CreatedAt:      now,
	}
	tx, err := s.db.Begin()
	if err != nil {
		return domain.Message{}, err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`
		INSERT INTO messages (id, conversation_id, role, content, created_at)
		VALUES ($1, $2, $3, $4, $5)`,
		message.ID, message.ConversationID, message.Role, message.Content, message.CreatedAt); err != nil {
		return domain.Message{}, err
	}
	if _, err := tx.Exec(`UPDATE conversations SET updated_at = $1 WHERE id = $2`, now, conversationID); err != nil {
		return domain.Message{}, err
	}
	return message, tx.Commit()
}

func (s *PostgresStore) UpdateConversationTitle(id string, title string) error {
	result, err := s.db.Exec(`
		UPDATE conversations
		SET title = $1, updated_at = $2
		WHERE id = $3`, normalizeTitle(title), time.Now().UTC(), id)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err == nil && affected == 0 {
		return errors.New("conversation not found")
	}
	return nil
}

func (s *PostgresStore) ListAgents() ([]domain.Agent, error) {
	rows, err := s.db.Query(`
		SELECT id, name, description, system_prompt, tools, created_at, updated_at
		FROM agents
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
	agent.CreatedAt = now
	agent.UpdatedAt = now

	toolsJSON, err := json.Marshal(agent.Tools)
	if err != nil {
		return domain.Agent{}, err
	}
	_, err = s.db.Exec(`
		INSERT INTO agents (id, name, description, system_prompt, tools, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		agent.ID, agent.Name, agent.Description, agent.SystemPrompt, toolsJSON, agent.CreatedAt, agent.UpdatedAt)
	return agent, err
}

func (s *PostgresStore) GetAgent(id string) (domain.Agent, bool, error) {
	row := s.db.QueryRow(`
		SELECT id, name, description, system_prompt, tools, created_at, updated_at
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
	agent.CreatedAt = existing.CreatedAt
	agent.UpdatedAt = time.Now().UTC()
	toolsJSON, err := json.Marshal(agent.Tools)
	if err != nil {
		return domain.Agent{}, err
	}
	_, err = s.db.Exec(`
		UPDATE agents
		SET name = $1, description = $2, system_prompt = $3, tools = $4, updated_at = $5
		WHERE id = $6`,
		agent.Name, agent.Description, agent.SystemPrompt, toolsJSON, agent.UpdatedAt, agent.ID)
	return agent, err
}

func (s *PostgresStore) GetDefaultAgent() (domain.Agent, bool, error) {
	if agent, ok, err := s.GetAgent("agent_planner"); err != nil || ok {
		return agent, ok, err
	}
	row := s.db.QueryRow(`
		SELECT id, name, description, system_prompt, tools, created_at, updated_at
		FROM agents
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

func (s *PostgresStore) CreateRun(agentID string, conversationID string) (domain.Run, error) {
	if _, ok, err := s.GetAgent(agentID); err != nil {
		return domain.Run{}, err
	} else if !ok {
		return domain.Run{}, errors.New("agent not found")
	}
	if _, ok, err := s.GetConversation(conversationID); err != nil {
		return domain.Run{}, err
	} else if !ok {
		return domain.Run{}, errors.New("conversation not found")
	}

	now := time.Now().UTC()
	run := domain.Run{
		ID:             newID("run"),
		AgentID:        agentID,
		ConversationID: conversationID,
		Status:         domain.RunQueued,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	_, err := s.db.Exec(`
		INSERT INTO runs (id, agent_id, conversation_id, status, error, started_at, completed_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		run.ID, run.AgentID, run.ConversationID, string(run.Status), run.Error, run.StartedAt, run.CompletedAt, run.CreatedAt, run.UpdatedAt)
	return run, err
}

func (s *PostgresStore) UpdateRunAgent(id string, agentID string) (domain.Run, error) {
	if _, ok, err := s.GetAgent(agentID); err != nil {
		return domain.Run{}, err
	} else if !ok {
		return domain.Run{}, errors.New("agent not found")
	}
	return s.scanRunQuery(`
		UPDATE runs
		SET agent_id = $1, updated_at = $2
		WHERE id = $3
		RETURNING id, agent_id, conversation_id, status, error, started_at, completed_at, created_at, updated_at`,
		agentID, time.Now().UTC(), id)
}

func (s *PostgresStore) UpdateRunStatus(id string, status domain.RunStatus, errorMessage string) (domain.Run, error) {
	now := time.Now().UTC()
	return s.scanRunQuery(`
		UPDATE runs
		SET status = $1,
			error = $2,
			started_at = CASE WHEN $1 = 'running' AND started_at IS NULL THEN $3 ELSE started_at END,
			completed_at = CASE
				WHEN $1 = 'waiting_for_user' THEN NULL
				WHEN $1 IN ('completed', 'failed', 'canceled') THEN $3
				ELSE completed_at
			END,
			updated_at = $3
		WHERE id = $4
		RETURNING id, agent_id, conversation_id, status, error, started_at, completed_at, created_at, updated_at`,
		string(status), strings.TrimSpace(errorMessage), now, id)
}

func (s *PostgresStore) GetRun(id string) (domain.Run, bool, error) {
	run, err := s.scanRunQuery(`
		SELECT id, agent_id, conversation_id, status, error, started_at, completed_at, created_at, updated_at
		FROM runs
		WHERE id = $1`, id)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Run{}, false, nil
	}
	if err != nil {
		return domain.Run{}, false, err
	}
	return run, true, nil
}

func (s *PostgresStore) ListRuns() ([]domain.Run, error) {
	rows, err := s.db.Query(`
		SELECT id, agent_id, conversation_id, status, error, started_at, completed_at, created_at, updated_at
		FROM runs
		ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []domain.Run{}
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, run)
	}
	return items, rows.Err()
}

func (s *PostgresStore) CreateCollaborationStep(step domain.CollaborationStep) (domain.CollaborationStep, error) {
	now := time.Now().UTC()
	step.ID = strings.TrimSpace(step.ID)
	if step.ID == "" {
		step.ID = newID("step")
	}
	step.Role = strings.TrimSpace(step.Role)
	if step.Role == "" {
		return domain.CollaborationStep{}, errors.New("collaboration role is required")
	}
	if step.Status == "" {
		step.Status = domain.CollaborationStepQueued
	}
	step.Input = strings.TrimSpace(step.Input)
	step.Output = strings.TrimSpace(step.Output)
	step.Error = strings.TrimSpace(step.Error)
	if step.Iteration < 0 {
		step.Iteration = 0
	}
	step.CreatedAt = now
	step.UpdatedAt = now

	_, err := s.db.Exec(`
		INSERT INTO collaboration_steps (id, run_id, conversation_id, role, agent_id, status, iteration, input, output, error, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		step.ID, step.RunID, step.ConversationID, step.Role, step.AgentID, string(step.Status), step.Iteration, step.Input, step.Output, step.Error, step.CreatedAt, step.UpdatedAt)
	return step, err
}

func (s *PostgresStore) UpdateCollaborationStep(id string, status domain.CollaborationStepStatus, output string, errorMessage string) (domain.CollaborationStep, error) {
	return s.scanStepQuery(`
		UPDATE collaboration_steps
		SET status = $1, output = $2, error = $3, updated_at = $4
		WHERE id = $5
		RETURNING id, run_id, conversation_id, role, agent_id, status, iteration, input, output, error, created_at, updated_at`,
		string(status), strings.TrimSpace(output), strings.TrimSpace(errorMessage), time.Now().UTC(), id)
}

func (s *PostgresStore) UpdateCollaborationStepOutput(id string, output string) (domain.CollaborationStep, error) {
	return s.scanStepQuery(`
		UPDATE collaboration_steps
		SET output = $1, updated_at = $2
		WHERE id = $3
		RETURNING id, run_id, conversation_id, role, agent_id, status, iteration, input, output, error, created_at, updated_at`,
		strings.TrimSpace(output), time.Now().UTC(), id)
}

func (s *PostgresStore) ListCollaborationSteps(runID string) ([]domain.CollaborationStep, error) {
	rows, err := s.db.Query(`
		SELECT id, run_id, conversation_id, role, agent_id, status, iteration, input, output, error, created_at, updated_at
		FROM collaboration_steps
		WHERE run_id = $1
		ORDER BY created_at ASC`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []domain.CollaborationStep{}
	for rows.Next() {
		step, err := scanStep(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, step)
	}
	return items, rows.Err()
}

func (s *PostgresStore) CreateTraceEvent(event domain.TraceEvent) (domain.TraceEvent, error) {
	event.ID = strings.TrimSpace(event.ID)
	if event.ID == "" {
		event.ID = newID("trace")
	}
	event.StepID = strings.TrimSpace(event.StepID)
	if event.Type == "" {
		return domain.TraceEvent{}, errors.New("trace event type is required")
	}
	if event.Payload == nil {
		event.Payload = map[string]any{}
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	if event.DurationMS < 0 {
		event.DurationMS = 0
	}
	payloadJSON, err := json.Marshal(event.Payload)
	if err != nil {
		return domain.TraceEvent{}, err
	}
	_, err = s.db.Exec(`
		INSERT INTO trace_events (id, run_id, step_id, type, payload, timestamp, duration_ms)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		event.ID, event.RunID, nullString(event.StepID), string(event.Type), payloadJSON, event.Timestamp, event.DurationMS)
	return event, err
}

func (s *PostgresStore) ListTraceEvents(runID string) ([]domain.TraceEvent, error) {
	rows, err := s.db.Query(`
		SELECT id, run_id, step_id, type, payload, timestamp, duration_ms
		FROM trace_events
		WHERE run_id = $1
		ORDER BY timestamp ASC, id ASC`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []domain.TraceEvent{}
	for rows.Next() {
		event, err := scanTraceEvent(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, event)
	}
	return items, rows.Err()
}

func (s *PostgresStore) GetRunTraceSummary(runID string) (domain.RunTraceSummary, error) {
	run, ok, err := s.GetRun(runID)
	if err != nil {
		return domain.RunTraceSummary{}, err
	}
	if !ok {
		return domain.RunTraceSummary{}, ErrNotFound("run")
	}
	events, err := s.ListTraceEvents(runID)
	if err != nil {
		return domain.RunTraceSummary{}, err
	}
	return buildRunTraceSummary(run, events), nil
}

func (s *PostgresStore) GetRunReplay(runID string) (domain.RunReplay, bool, error) {
	run, ok, err := s.GetRun(runID)
	if err != nil || !ok {
		return domain.RunReplay{}, ok, err
	}
	conversation, ok, err := s.GetConversation(run.ConversationID)
	if err != nil {
		return domain.RunReplay{}, false, err
	}
	if !ok {
		return domain.RunReplay{}, false, errors.New("conversation not found")
	}
	messages, err := s.ListMessages(run.ConversationID)
	if err != nil {
		return domain.RunReplay{}, false, err
	}
	steps, err := s.ListCollaborationSteps(runID)
	if err != nil {
		return domain.RunReplay{}, false, err
	}
	events, err := s.ListTraceEvents(runID)
	if err != nil {
		return domain.RunReplay{}, false, err
	}
	return domain.RunReplay{
		Run:          run,
		Conversation: conversation,
		Messages:     messages,
		Steps:        steps,
		Summary:      buildRunTraceSummary(run, events),
		Events:       events,
	}, true, nil
}

func (s *PostgresStore) CreateMemory(memory domain.Memory, embedding domain.MemoryEmbedding) (domain.Memory, error) {
	now := time.Now().UTC()
	memory.ID = strings.TrimSpace(memory.ID)
	if memory.ID == "" {
		memory.ID = newID("mem")
	}
	memory.Kind = strings.TrimSpace(memory.Kind)
	if memory.Kind == "" {
		return domain.Memory{}, errors.New("memory kind is required")
	}
	memory.Content = strings.TrimSpace(memory.Content)
	if memory.Content == "" {
		return domain.Memory{}, errors.New("memory content is required")
	}
	if memory.Metadata == nil {
		memory.Metadata = map[string]any{}
	}
	if memory.CreatedAt.IsZero() {
		memory.CreatedAt = now
	}
	memory.UpdatedAt = now
	embedding.MemoryID = memory.ID
	if embedding.Provider == "" {
		embedding.Provider = "local"
	}
	if embedding.Model == "" {
		embedding.Model = "local_hash"
	}
	if embedding.Dimensions == 0 {
		embedding.Dimensions = len(embedding.Embedding)
	}
	if embedding.CreatedAt.IsZero() {
		embedding.CreatedAt = now
	}
	if len(embedding.Embedding) != 1536 {
		return domain.Memory{}, fmt.Errorf("memory embedding dimensions must be 1536, got %d", len(embedding.Embedding))
	}
	metadataJSON, err := json.Marshal(memory.Metadata)
	if err != nil {
		return domain.Memory{}, err
	}

	tx, err := s.db.Begin()
	if err != nil {
		return domain.Memory{}, err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`
		INSERT INTO memories (id, workspace_id, user_id, project_id, conversation_id, run_id, source_message_id, kind, content, metadata, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT (id) DO UPDATE SET
			workspace_id = EXCLUDED.workspace_id,
			user_id = EXCLUDED.user_id,
			project_id = EXCLUDED.project_id,
			conversation_id = EXCLUDED.conversation_id,
			run_id = EXCLUDED.run_id,
			source_message_id = EXCLUDED.source_message_id,
			kind = EXCLUDED.kind,
			content = EXCLUDED.content,
			metadata = EXCLUDED.metadata,
			updated_at = EXCLUDED.updated_at`,
		memory.ID, nullString(memory.WorkspaceID), nullString(memory.UserID), nullString(memory.ProjectID), nullString(memory.ConversationID), nullString(memory.RunID), nullString(memory.SourceMessageID), memory.Kind, memory.Content, metadataJSON, memory.CreatedAt, memory.UpdatedAt); err != nil {
		return domain.Memory{}, err
	}

	if _, err := tx.Exec(`
		INSERT INTO memory_embeddings (memory_id, provider, model, dimensions, embedding, created_at)
		VALUES ($1, $2, $3, $4, $5::vector, $6)
		ON CONFLICT (memory_id) DO UPDATE SET
			provider = EXCLUDED.provider,
			model = EXCLUDED.model,
			dimensions = EXCLUDED.dimensions,
			embedding = EXCLUDED.embedding,
			created_at = EXCLUDED.created_at`,
		embedding.MemoryID, embedding.Provider, embedding.Model, embedding.Dimensions, vectorLiteral(embedding.Embedding), embedding.CreatedAt); err != nil {
		return domain.Memory{}, err
	}
	return memory, tx.Commit()
}

func (s *PostgresStore) SearchMemories(search domain.MemorySearch) ([]domain.RetrievedMemory, error) {
	if len(search.Embedding) != 1536 {
		return nil, fmt.Errorf("memory search embedding dimensions must be 1536, got %d", len(search.Embedding))
	}
	limit := search.Limit
	if limit <= 0 || limit > 10 {
		limit = 5
	}

	args := []any{vectorLiteral(search.Embedding), limit}
	conditions := []string{}
	if strings.TrimSpace(search.WorkspaceID) != "" {
		args = append(args, search.WorkspaceID)
		conditions = append(conditions, fmt.Sprintf("m.workspace_id = $%d", len(args)))
	}
	if strings.TrimSpace(search.UserID) != "" {
		args = append(args, search.UserID)
		conditions = append(conditions, fmt.Sprintf("m.user_id = $%d", len(args)))
	}
	if strings.TrimSpace(search.ProjectID) != "" {
		args = append(args, search.ProjectID)
		conditions = append(conditions, fmt.Sprintf("m.project_id = $%d", len(args)))
	}
	for key, value := range search.Metadata {
		args = append(args, key, value)
		conditions = append(conditions, fmt.Sprintf("m.metadata ->> $%d = $%d", len(args)-1, len(args)))
	}
	where := ""
	if len(conditions) > 0 {
		where = "WHERE " + strings.Join(conditions, " AND ")
	}

	query := `
		SELECT
			m.id, m.workspace_id, m.user_id, m.project_id, m.conversation_id, m.run_id, m.source_message_id, m.kind, m.content, m.metadata, m.created_at, m.updated_at,
			1 - (e.embedding <=> $1::vector) AS similarity,
			0.05 / (1 + GREATEST(EXTRACT(EPOCH FROM (now() - m.created_at)) / 86400, 0) / 30) AS recency_boost,
			(1 - (e.embedding <=> $1::vector)) + (0.05 / (1 + GREATEST(EXTRACT(EPOCH FROM (now() - m.created_at)) / 86400, 0) / 30)) AS score
		FROM memories m
		JOIN memory_embeddings e ON e.memory_id = m.id
		` + where + `
		ORDER BY score DESC
		LIMIT $2`
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []domain.RetrievedMemory{}
	for rows.Next() {
		item, err := scanRetrievedMemory(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *PostgresStore) migrate(ctx context.Context) error {
	for _, statement := range postgresMigrations {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
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
				INSERT INTO agents (id, name, description, system_prompt, tools, created_at, updated_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7)`,
				agent.ID, agent.Name, agent.Description, agent.SystemPrompt, toolsJSON, agent.CreatedAt, agent.UpdatedAt); err != nil {
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

func (s *PostgresStore) scanRunQuery(query string, args ...any) (domain.Run, error) {
	run, err := scanRun(s.db.QueryRow(query, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Run{}, errors.New("run not found")
	}
	return run, err
}

func (s *PostgresStore) scanStepQuery(query string, args ...any) (domain.CollaborationStep, error) {
	step, err := scanStep(s.db.QueryRow(query, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.CollaborationStep{}, errors.New("collaboration step not found")
	}
	return step, err
}

type scanner interface {
	Scan(dest ...any) error
}

func scanAgent(row scanner) (domain.Agent, error) {
	var agent domain.Agent
	var toolsJSON []byte
	if err := row.Scan(&agent.ID, &agent.Name, &agent.Description, &agent.SystemPrompt, &toolsJSON, &agent.CreatedAt, &agent.UpdatedAt); err != nil {
		return domain.Agent{}, err
	}
	if len(toolsJSON) > 0 {
		if err := json.Unmarshal(toolsJSON, &agent.Tools); err != nil {
			return domain.Agent{}, err
		}
	}
	return agent, nil
}

func scanRun(row scanner) (domain.Run, error) {
	var run domain.Run
	var status string
	var errorMessage sql.NullString
	var startedAt sql.NullTime
	var completedAt sql.NullTime
	if err := row.Scan(&run.ID, &run.AgentID, &run.ConversationID, &status, &errorMessage, &startedAt, &completedAt, &run.CreatedAt, &run.UpdatedAt); err != nil {
		return domain.Run{}, err
	}
	run.Status = domain.RunStatus(status)
	if errorMessage.Valid {
		run.Error = errorMessage.String
	}
	if startedAt.Valid {
		run.StartedAt = &startedAt.Time
	}
	if completedAt.Valid {
		run.CompletedAt = &completedAt.Time
	}
	return run, nil
}

func scanStep(row scanner) (domain.CollaborationStep, error) {
	var step domain.CollaborationStep
	var status string
	var agentID sql.NullString
	if err := row.Scan(&step.ID, &step.RunID, &step.ConversationID, &step.Role, &agentID, &status, &step.Iteration, &step.Input, &step.Output, &step.Error, &step.CreatedAt, &step.UpdatedAt); err != nil {
		return domain.CollaborationStep{}, err
	}
	if agentID.Valid {
		step.AgentID = agentID.String
	}
	step.Status = domain.CollaborationStepStatus(status)
	return step, nil
}

func scanTraceEvent(row scanner) (domain.TraceEvent, error) {
	var event domain.TraceEvent
	var eventType string
	var stepID sql.NullString
	var payloadJSON []byte
	if err := row.Scan(&event.ID, &event.RunID, &stepID, &eventType, &payloadJSON, &event.Timestamp, &event.DurationMS); err != nil {
		return domain.TraceEvent{}, err
	}
	if stepID.Valid {
		event.StepID = stepID.String
	}
	event.Type = domain.TraceEventType(eventType)
	if len(payloadJSON) > 0 {
		if err := json.Unmarshal(payloadJSON, &event.Payload); err != nil {
			return domain.TraceEvent{}, err
		}
	}
	if event.Payload == nil {
		event.Payload = map[string]any{}
	}
	return event, nil
}

func scanRetrievedMemory(row scanner) (domain.RetrievedMemory, error) {
	var item domain.RetrievedMemory
	var metadataJSON []byte
	var workspaceID sql.NullString
	var userID sql.NullString
	var projectID sql.NullString
	var conversationID sql.NullString
	var runID sql.NullString
	var sourceMessageID sql.NullString
	if err := row.Scan(
		&item.Memory.ID,
		&workspaceID,
		&userID,
		&projectID,
		&conversationID,
		&runID,
		&sourceMessageID,
		&item.Memory.Kind,
		&item.Memory.Content,
		&metadataJSON,
		&item.Memory.CreatedAt,
		&item.Memory.UpdatedAt,
		&item.Similarity,
		&item.RecencyBoost,
		&item.Score,
	); err != nil {
		return domain.RetrievedMemory{}, err
	}
	if workspaceID.Valid {
		item.Memory.WorkspaceID = workspaceID.String
	}
	if userID.Valid {
		item.Memory.UserID = userID.String
	}
	if projectID.Valid {
		item.Memory.ProjectID = projectID.String
	}
	if conversationID.Valid {
		item.Memory.ConversationID = conversationID.String
	}
	if runID.Valid {
		item.Memory.RunID = runID.String
	}
	if sourceMessageID.Valid {
		item.Memory.SourceMessageID = sourceMessageID.String
	}
	if len(metadataJSON) > 0 {
		if err := json.Unmarshal(metadataJSON, &item.Memory.Metadata); err != nil {
			return domain.RetrievedMemory{}, err
		}
	}
	if item.Memory.Metadata == nil {
		item.Memory.Metadata = map[string]any{}
	}
	return item, nil
}

func nullString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func vectorLiteral(values []float64) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, strconv.FormatFloat(value, 'f', -1, 64))
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func defaultAgentByID(id string) domain.Agent {
	for _, agent := range defaultAgents(time.Now().UTC()) {
		if agent.ID == id {
			return agent
		}
	}
	return domain.Agent{}
}

var postgresMigrations = []string{
	`CREATE EXTENSION IF NOT EXISTS vector`,
	`CREATE TABLE IF NOT EXISTS conversations (
		id text PRIMARY KEY,
		workspace_id text,
		user_id text,
		project_id text,
		title text NOT NULL,
		created_at timestamptz NOT NULL,
		updated_at timestamptz NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS agents (
		id text PRIMARY KEY,
		workspace_id text,
		user_id text,
		project_id text,
		name text NOT NULL,
		description text NOT NULL DEFAULT '',
		system_prompt text NOT NULL DEFAULT '',
		tools jsonb NOT NULL DEFAULT '[]'::jsonb,
		created_at timestamptz NOT NULL,
		updated_at timestamptz NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS messages (
		id text PRIMARY KEY,
		conversation_id text NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
		workspace_id text,
		user_id text,
		project_id text,
		role text NOT NULL,
		content text NOT NULL,
		created_at timestamptz NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS runs (
		id text PRIMARY KEY,
		agent_id text NOT NULL REFERENCES agents(id),
		conversation_id text NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
		workspace_id text,
		user_id text,
		project_id text,
		status text NOT NULL,
		error text NOT NULL DEFAULT '',
		started_at timestamptz,
		completed_at timestamptz,
		created_at timestamptz NOT NULL,
		updated_at timestamptz NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS collaboration_steps (
		id text PRIMARY KEY,
		run_id text NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
		conversation_id text NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
		workspace_id text,
		user_id text,
		project_id text,
		role text NOT NULL,
		agent_id text NOT NULL DEFAULT '',
		status text NOT NULL,
		iteration integer NOT NULL DEFAULT 0,
		input text NOT NULL DEFAULT '',
		output text NOT NULL DEFAULT '',
		error text NOT NULL DEFAULT '',
		created_at timestamptz NOT NULL,
		updated_at timestamptz NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS trace_events (
		id text PRIMARY KEY,
		run_id text NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
		step_id text,
		workspace_id text,
		user_id text,
		project_id text,
		type text NOT NULL,
		payload jsonb NOT NULL DEFAULT '{}'::jsonb,
		timestamp timestamptz NOT NULL,
		duration_ms bigint NOT NULL DEFAULT 0
	)`,
	`CREATE TABLE IF NOT EXISTS memories (
		id text PRIMARY KEY,
		workspace_id text,
		user_id text,
		project_id text,
		conversation_id text REFERENCES conversations(id) ON DELETE SET NULL,
		run_id text REFERENCES runs(id) ON DELETE SET NULL,
		source_message_id text REFERENCES messages(id) ON DELETE SET NULL,
		kind text NOT NULL,
		content text NOT NULL,
		metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
		created_at timestamptz NOT NULL,
		updated_at timestamptz NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS memory_embeddings (
		memory_id text PRIMARY KEY REFERENCES memories(id) ON DELETE CASCADE,
		provider text NOT NULL,
		model text NOT NULL,
		dimensions integer NOT NULL,
		embedding vector(1536) NOT NULL,
		created_at timestamptz NOT NULL
	)`,
	`DO $$
	BEGIN
		IF EXISTS (
			SELECT 1
			FROM information_schema.columns
			WHERE table_schema = 'public'
				AND table_name = 'memory_embeddings'
				AND column_name = 'embedding'
				AND udt_name <> 'vector'
		) THEN
			DELETE FROM memory_embeddings;
			ALTER TABLE memory_embeddings DROP COLUMN embedding;
		END IF;
	END $$`,
	`ALTER TABLE memory_embeddings ADD COLUMN IF NOT EXISTS embedding vector(1536) NOT NULL`,
	`CREATE INDEX IF NOT EXISTS idx_runs_conversation_created ON runs(conversation_id, created_at DESC)`,
	`CREATE INDEX IF NOT EXISTS idx_runs_status_created ON runs(status, created_at DESC)`,
	`CREATE INDEX IF NOT EXISTS idx_messages_conversation_created ON messages(conversation_id, created_at ASC)`,
	`CREATE INDEX IF NOT EXISTS idx_steps_run_created ON collaboration_steps(run_id, created_at ASC)`,
	`CREATE INDEX IF NOT EXISTS idx_trace_events_run_timestamp ON trace_events(run_id, timestamp ASC)`,
	`CREATE INDEX IF NOT EXISTS idx_trace_events_type_timestamp ON trace_events(type, timestamp DESC)`,
	`CREATE INDEX IF NOT EXISTS idx_memories_scope_created ON memories(workspace_id, user_id, project_id, created_at DESC)`,
	`CREATE INDEX IF NOT EXISTS idx_memory_embeddings_vector ON memory_embeddings USING hnsw (embedding vector_cosine_ops)`,
}

var _ Store = (*PostgresStore)(nil)
