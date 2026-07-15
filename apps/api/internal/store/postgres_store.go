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
		return ErrNotFound("conversation")
	}
	return nil
}

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

func (s *PostgresStore) CreateRun(agentID string, conversationID string, snapshot domain.RuntimeSnapshot) (domain.Run, error) {
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
	if snapshot.SchemaVersion != domain.CurrentRuntimeSnapshotVersion {
		return domain.Run{}, errors.New("runtime snapshot is required")
	}
	snapshotJSON, err := json.Marshal(snapshot)
	if err != nil {
		return domain.Run{}, err
	}

	now := time.Now().UTC()
	run := domain.Run{
		ID:              newID("run"),
		AgentID:         agentID,
		ConversationID:  conversationID,
		Status:          domain.RunQueued,
		RuntimeSnapshot: cloneRuntimeSnapshot(snapshot),
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	_, err = s.db.Exec(`
		INSERT INTO runs (id, agent_id, conversation_id, status, error, runtime_snapshot, started_at, heartbeat_at, completed_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		run.ID, run.AgentID, run.ConversationID, string(run.Status), run.Error, snapshotJSON, run.StartedAt, run.HeartbeatAt, run.CompletedAt, run.CreatedAt, run.UpdatedAt)
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
		RETURNING id, agent_id, conversation_id, status, error, runtime_snapshot, started_at, heartbeat_at, completed_at, created_at, updated_at`,
		agentID, time.Now().UTC(), id)
}

func (s *PostgresStore) UpdateRunStatus(id string, status domain.RunStatus, errorMessage string) (domain.Run, error) {
	now := time.Now().UTC()
	return s.scanRunQuery(`
		UPDATE runs
		SET status = $1,
			error = $2,
			started_at = CASE WHEN $1 = 'running' AND started_at IS NULL THEN $3 ELSE started_at END,
			heartbeat_at = CASE WHEN $1 = 'running' THEN $3 ELSE heartbeat_at END,
			completed_at = CASE
				WHEN $1 = 'waiting_for_user' THEN NULL
				WHEN $1 = 'running' THEN NULL
				WHEN $1 IN ('completed', 'failed', 'failed_recoverable', 'canceled') THEN $3
				ELSE completed_at
			END,
			updated_at = $3
		WHERE id = $4
		RETURNING id, agent_id, conversation_id, status, error, runtime_snapshot, started_at, heartbeat_at, completed_at, created_at, updated_at`,
		string(status), strings.TrimSpace(errorMessage), now, id)
}

func (s *PostgresStore) UpdateRunHeartbeat(id string) (domain.Run, error) {
	now := time.Now().UTC()
	return s.scanRunQuery(`
		UPDATE runs
		SET heartbeat_at = $1, updated_at = $1
		WHERE id = $2
		RETURNING id, agent_id, conversation_id, status, error, runtime_snapshot, started_at, heartbeat_at, completed_at, created_at, updated_at`,
		now, id)
}

func (s *PostgresStore) ListStaleRunningRuns(cutoff time.Time) ([]domain.Run, error) {
	rows, err := s.db.Query(`
		SELECT id, agent_id, conversation_id, status, error, runtime_snapshot, started_at, heartbeat_at, completed_at, created_at, updated_at
		FROM runs
		WHERE status = 'running'
			AND (heartbeat_at IS NULL OR heartbeat_at < $1)
		ORDER BY created_at ASC`, cutoff)
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

func (s *PostgresStore) GetRun(id string) (domain.Run, bool, error) {
	run, err := s.scanRunQuery(`
		SELECT id, agent_id, conversation_id, status, error, runtime_snapshot, started_at, heartbeat_at, completed_at, created_at, updated_at
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
		SELECT id, agent_id, conversation_id, status, error, runtime_snapshot, started_at, heartbeat_at, completed_at, created_at, updated_at
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

func (s *PostgresStore) CreateRunEvent(event domain.RunEvent) (domain.RunEvent, error) {
	event.ID = strings.TrimSpace(event.ID)
	if event.ID == "" {
		event.ID = newID("event")
	}
	if event.Type == "" {
		return domain.RunEvent{}, errors.New("run event type is required")
	}
	if event.SchemaVersion == 0 {
		event.SchemaVersion = domain.CurrentRunEventSchemaVersion
	}
	if event.Payload == nil {
		event.Payload = map[string]any{}
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	payload, err := json.Marshal(event.Payload)
	if err != nil {
		return domain.RunEvent{}, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return domain.RunEvent{}, err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`SELECT pg_advisory_xact_lock(hashtext($1))`, event.RunID); err != nil {
		return domain.RunEvent{}, err
	}
	err = tx.QueryRow(`
		INSERT INTO run_events (id, run_id, conversation_id, stage_id, turn_id, parent_event_id, type, schema_version, sequence, payload, timestamp)
		VALUES ($1,$2,NULLIF($3,''),NULLIF($4,''),NULLIF($5,''),NULLIF($6,''),$7,$8,
			(SELECT COALESCE(MAX(sequence),0)+1 FROM run_events WHERE run_id=$2),$9,$10)
		RETURNING sequence`, event.ID, event.RunID, event.ConversationID, event.StageID, event.TurnID,
		event.ParentEventID, string(event.Type), event.SchemaVersion, payload, event.Timestamp).Scan(&event.Sequence)
	if err != nil {
		return domain.RunEvent{}, err
	}
	return event, tx.Commit()
}

func (s *PostgresStore) ListRunEvents(runID string) ([]domain.RunEvent, error) {
	rows, err := s.db.Query(`SELECT id,run_id,conversation_id,stage_id,turn_id,parent_event_id,type,schema_version,sequence,payload,timestamp FROM run_events WHERE run_id=$1 ORDER BY sequence`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []domain.RunEvent{}
	for rows.Next() {
		var item domain.RunEvent
		var conversationID, stageID, turnID, parentID sql.NullString
		var eventType string
		var payload []byte
		if err := rows.Scan(&item.ID, &item.RunID, &conversationID, &stageID, &turnID, &parentID, &eventType, &item.SchemaVersion, &item.Sequence, &payload, &item.Timestamp); err != nil {
			return nil, err
		}
		if conversationID.Valid {
			item.ConversationID = conversationID.String
		}
		if stageID.Valid {
			item.StageID = stageID.String
		}
		if turnID.Valid {
			item.TurnID = turnID.String
		}
		if parentID.Valid {
			item.ParentEventID = parentID.String
		}
		item.Type = domain.RunEventType(eventType)
		if err := json.Unmarshal(payload, &item.Payload); err != nil {
			return nil, err
		}
		items = append(items, item)
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
	events, err := s.ListRunEvents(runID)
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
	runEvents, err := s.ListRunEvents(runID)
	if err != nil {
		return domain.RunReplay{}, false, err
	}
	return domain.RunReplay{
		Run:             run,
		RuntimeSnapshot: cloneRuntimeSnapshotValue(run.RuntimeSnapshot),
		Conversation:    conversation,
		Messages:        messages,
		Steps:           steps,
		Summary:         buildRunTraceSummary(run, runEvents),
		RunEvents:       runEvents,
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
	if limit <= 0 {
		limit = 5
	} else if limit > 20 {
		limit = 20
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
	if strings.TrimSpace(search.EmbeddingProvider) != "" {
		args = append(args, search.EmbeddingProvider)
		conditions = append(conditions, fmt.Sprintf("e.provider = $%d", len(args)))
	}
	if strings.TrimSpace(search.EmbeddingModel) != "" {
		args = append(args, search.EmbeddingModel)
		conditions = append(conditions, fmt.Sprintf("e.model = $%d", len(args)))
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

func (s *PostgresStore) CreateDocument(document domain.Document, chunks []domain.DocumentChunk, embeddings []domain.DocumentChunkEmbedding) (domain.Document, error) {
	if len(chunks) != len(embeddings) {
		return domain.Document{}, errors.New("document chunks and embeddings length mismatch")
	}
	now := time.Now().UTC()
	document.ID = strings.TrimSpace(document.ID)
	if document.ID == "" {
		document.ID = newID("doc")
	}
	document.Title = strings.TrimSpace(document.Title)
	if document.Title == "" {
		return domain.Document{}, errors.New("document title is required")
	}
	document.Content = strings.TrimSpace(document.Content)
	if document.Content == "" {
		return domain.Document{}, errors.New("document content is required")
	}
	document.SourceType = strings.TrimSpace(document.SourceType)
	if document.SourceType == "" {
		document.SourceType = "text"
	}
	if document.Metadata == nil {
		document.Metadata = map[string]any{}
	}
	if document.CreatedAt.IsZero() {
		document.CreatedAt = now
	}
	document.UpdatedAt = now
	metadataJSON, err := json.Marshal(document.Metadata)
	if err != nil {
		return domain.Document{}, err
	}

	tx, err := s.db.Begin()
	if err != nil {
		return domain.Document{}, err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`
		INSERT INTO documents (id, workspace_id, title, source_type, source_uri, mime_type, content, metadata, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		document.ID, nullString(document.WorkspaceID), document.Title, document.SourceType, nullString(document.SourceURI), nullString(document.MimeType), document.Content, metadataJSON, document.CreatedAt, document.UpdatedAt); err != nil {
		return domain.Document{}, err
	}

	for i := range chunks {
		chunk := chunks[i]
		chunk.ID = strings.TrimSpace(chunk.ID)
		if chunk.ID == "" {
			chunk.ID = newID("chunk")
		}
		chunk.DocumentID = document.ID
		chunk.ChunkIndex = i
		chunk.Content = strings.TrimSpace(chunk.Content)
		if chunk.Content == "" {
			return domain.Document{}, errors.New("document chunk content is required")
		}
		if chunk.Metadata == nil {
			chunk.Metadata = map[string]any{}
		}
		if chunk.CreatedAt.IsZero() {
			chunk.CreatedAt = now
		}
		chunkMetadataJSON, err := json.Marshal(chunk.Metadata)
		if err != nil {
			return domain.Document{}, err
		}
		if _, err := tx.Exec(`
			INSERT INTO document_chunks (id, document_id, chunk_index, content, token_count, metadata, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			chunk.ID, chunk.DocumentID, chunk.ChunkIndex, chunk.Content, chunk.TokenCount, chunkMetadataJSON, chunk.CreatedAt); err != nil {
			return domain.Document{}, err
		}

		embedding := embeddings[i]
		embedding.ChunkID = chunk.ID
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
			return domain.Document{}, fmt.Errorf("document chunk embedding dimensions must be 1536, got %d", len(embedding.Embedding))
		}
		if _, err := tx.Exec(`
			INSERT INTO document_chunk_embeddings (chunk_id, provider, model, dimensions, embedding, created_at)
			VALUES ($1, $2, $3, $4, $5::vector, $6)`,
			embedding.ChunkID, embedding.Provider, embedding.Model, embedding.Dimensions, vectorLiteral(embedding.Embedding), embedding.CreatedAt); err != nil {
			return domain.Document{}, err
		}
	}

	document.ChunkCount = len(chunks)
	document.EmbeddingCount = len(embeddings)
	return document, tx.Commit()
}

func (s *PostgresStore) ListDocuments() ([]domain.Document, error) {
	rows, err := s.db.Query(`
		SELECT d.id, d.workspace_id, d.title, d.source_type, d.source_uri, d.mime_type, d.metadata, d.created_at, d.updated_at,
			COUNT(DISTINCT c.id) AS chunk_count,
			COUNT(e.chunk_id) AS embedding_count
		FROM documents d
		LEFT JOIN document_chunks c ON c.document_id = d.id
		LEFT JOIN document_chunk_embeddings e ON e.chunk_id = c.id
		GROUP BY d.id
		ORDER BY d.created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []domain.Document{}
	for rows.Next() {
		document, err := scanDocument(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, document)
	}
	return items, rows.Err()
}

func (s *PostgresStore) GetDocument(id string) (domain.Document, []domain.DocumentChunk, bool, error) {
	row := s.db.QueryRow(`
		SELECT d.id, d.workspace_id, d.title, d.source_type, d.source_uri, d.mime_type, d.metadata, d.created_at, d.updated_at,
			COUNT(DISTINCT c.id) AS chunk_count,
			COUNT(e.chunk_id) AS embedding_count
		FROM documents d
		LEFT JOIN document_chunks c ON c.document_id = d.id
		LEFT JOIN document_chunk_embeddings e ON e.chunk_id = c.id
		WHERE d.id = $1
		GROUP BY d.id`, strings.TrimSpace(id))
	document, err := scanDocument(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Document{}, nil, false, nil
	}
	if err != nil {
		return domain.Document{}, nil, false, err
	}

	rows, err := s.db.Query(`
		SELECT id, document_id, chunk_index, content, token_count, metadata, created_at
		FROM document_chunks
		WHERE document_id = $1
		ORDER BY chunk_index ASC`, document.ID)
	if err != nil {
		return domain.Document{}, nil, false, err
	}
	defer rows.Close()

	chunks := []domain.DocumentChunk{}
	for rows.Next() {
		chunk, err := scanDocumentChunk(rows)
		if err != nil {
			return domain.Document{}, nil, false, err
		}
		chunk.Document = document
		chunks = append(chunks, chunk)
	}
	return document, chunks, true, rows.Err()
}

func (s *PostgresStore) DeleteDocument(id string) error {
	result, err := s.db.Exec(`DELETE FROM documents WHERE id = $1`, strings.TrimSpace(id))
	if err != nil {
		return err
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if deleted == 0 {
		return ErrNotFound("document")
	}
	return nil
}

func (s *PostgresStore) SearchDocumentChunks(search domain.DocumentSearch) ([]domain.RetrievedDocumentChunk, error) {
	if len(search.Embedding) != 1536 {
		return nil, fmt.Errorf("document search embedding dimensions must be 1536, got %d", len(search.Embedding))
	}
	limit := search.Limit
	if limit <= 0 || limit > 10 {
		limit = 5
	}

	args := []any{vectorLiteral(search.Embedding), limit}
	conditions := []string{}
	if strings.TrimSpace(search.WorkspaceID) != "" {
		args = append(args, search.WorkspaceID)
		conditions = append(conditions, fmt.Sprintf("d.workspace_id = $%d", len(args)))
	}
	if strings.TrimSpace(search.EmbeddingProvider) != "" {
		args = append(args, search.EmbeddingProvider)
		conditions = append(conditions, fmt.Sprintf("e.provider = $%d", len(args)))
	}
	if strings.TrimSpace(search.EmbeddingModel) != "" {
		args = append(args, search.EmbeddingModel)
		conditions = append(conditions, fmt.Sprintf("e.model = $%d", len(args)))
	}
	for key, value := range search.Metadata {
		args = append(args, key, value)
		conditions = append(conditions, fmt.Sprintf("(c.metadata ->> $%d = $%d OR d.metadata ->> $%d = $%d)", len(args)-1, len(args), len(args)-1, len(args)))
	}
	if search.MinSimilarity > 0 {
		args = append(args, search.MinSimilarity)
		conditions = append(conditions, fmt.Sprintf("(1 - (e.embedding <=> $1::vector)) >= $%d", len(args)))
	}
	where := ""
	if len(conditions) > 0 {
		where = "WHERE " + strings.Join(conditions, " AND ")
	}

	query := `
		SELECT
			d.id, d.workspace_id, d.title, d.source_type, d.source_uri, d.mime_type, d.metadata, d.created_at, d.updated_at,
			c.id, c.document_id, c.chunk_index, c.content, c.token_count, c.metadata, c.created_at,
			1 - (e.embedding <=> $1::vector) AS similarity,
			0.03 / (1 + GREATEST(EXTRACT(EPOCH FROM (now() - c.created_at)) / 86400, 0) / 30) AS recency_boost,
			(1 - (e.embedding <=> $1::vector)) + (0.03 / (1 + GREATEST(EXTRACT(EPOCH FROM (now() - c.created_at)) / 86400, 0) / 30)) AS score
		FROM document_chunks c
		JOIN documents d ON d.id = c.document_id
		JOIN document_chunk_embeddings e ON e.chunk_id = c.id
		` + where + `
		ORDER BY score DESC
		LIMIT $2`
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []domain.RetrievedDocumentChunk{}
	for rows.Next() {
		item, err := scanRetrievedDocumentChunk(rows)
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

func scanRun(row scanner) (domain.Run, error) {
	var run domain.Run
	var status string
	var errorMessage sql.NullString
	var startedAt sql.NullTime
	var heartbeatAt sql.NullTime
	var completedAt sql.NullTime
	var snapshotJSON []byte
	if err := row.Scan(&run.ID, &run.AgentID, &run.ConversationID, &status, &errorMessage, &snapshotJSON, &startedAt, &heartbeatAt, &completedAt, &run.CreatedAt, &run.UpdatedAt); err != nil {
		return domain.Run{}, err
	}
	if len(snapshotJSON) > 0 && string(snapshotJSON) != "null" {
		var snapshot domain.RuntimeSnapshot
		if err := json.Unmarshal(snapshotJSON, &snapshot); err != nil {
			return domain.Run{}, err
		}
		run.RuntimeSnapshot = &snapshot
	}
	run.Status = domain.RunStatus(status)
	if errorMessage.Valid {
		run.Error = errorMessage.String
	}
	if startedAt.Valid {
		run.StartedAt = &startedAt.Time
	}
	if heartbeatAt.Valid {
		run.HeartbeatAt = &heartbeatAt.Time
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

func scanDocument(row scanner) (domain.Document, error) {
	var document domain.Document
	var workspaceID sql.NullString
	var sourceURI sql.NullString
	var mimeType sql.NullString
	var metadataJSON []byte
	if err := row.Scan(
		&document.ID,
		&workspaceID,
		&document.Title,
		&document.SourceType,
		&sourceURI,
		&mimeType,
		&metadataJSON,
		&document.CreatedAt,
		&document.UpdatedAt,
		&document.ChunkCount,
		&document.EmbeddingCount,
	); err != nil {
		return domain.Document{}, err
	}
	if workspaceID.Valid {
		document.WorkspaceID = workspaceID.String
	}
	if sourceURI.Valid {
		document.SourceURI = sourceURI.String
	}
	if mimeType.Valid {
		document.MimeType = mimeType.String
	}
	if len(metadataJSON) > 0 {
		if err := json.Unmarshal(metadataJSON, &document.Metadata); err != nil {
			return domain.Document{}, err
		}
	}
	if document.Metadata == nil {
		document.Metadata = map[string]any{}
	}
	return document, nil
}

func scanDocumentChunk(row scanner) (domain.DocumentChunk, error) {
	var chunk domain.DocumentChunk
	var metadataJSON []byte
	if err := row.Scan(
		&chunk.ID,
		&chunk.DocumentID,
		&chunk.ChunkIndex,
		&chunk.Content,
		&chunk.TokenCount,
		&metadataJSON,
		&chunk.CreatedAt,
	); err != nil {
		return domain.DocumentChunk{}, err
	}
	if len(metadataJSON) > 0 {
		if err := json.Unmarshal(metadataJSON, &chunk.Metadata); err != nil {
			return domain.DocumentChunk{}, err
		}
	}
	if chunk.Metadata == nil {
		chunk.Metadata = map[string]any{}
	}
	return chunk, nil
}

func scanRetrievedDocumentChunk(row scanner) (domain.RetrievedDocumentChunk, error) {
	var item domain.RetrievedDocumentChunk
	var workspaceID sql.NullString
	var sourceURI sql.NullString
	var mimeType sql.NullString
	var documentMetadataJSON []byte
	var chunkMetadataJSON []byte
	if err := row.Scan(
		&item.Document.ID,
		&workspaceID,
		&item.Document.Title,
		&item.Document.SourceType,
		&sourceURI,
		&mimeType,
		&documentMetadataJSON,
		&item.Document.CreatedAt,
		&item.Document.UpdatedAt,
		&item.Chunk.ID,
		&item.Chunk.DocumentID,
		&item.Chunk.ChunkIndex,
		&item.Chunk.Content,
		&item.Chunk.TokenCount,
		&chunkMetadataJSON,
		&item.Chunk.CreatedAt,
		&item.Similarity,
		&item.RecencyBoost,
		&item.Score,
	); err != nil {
		return domain.RetrievedDocumentChunk{}, err
	}
	if workspaceID.Valid {
		item.Document.WorkspaceID = workspaceID.String
	}
	if sourceURI.Valid {
		item.Document.SourceURI = sourceURI.String
	}
	if mimeType.Valid {
		item.Document.MimeType = mimeType.String
	}
	if len(documentMetadataJSON) > 0 {
		if err := json.Unmarshal(documentMetadataJSON, &item.Document.Metadata); err != nil {
			return domain.RetrievedDocumentChunk{}, err
		}
	}
	if len(chunkMetadataJSON) > 0 {
		if err := json.Unmarshal(chunkMetadataJSON, &item.Chunk.Metadata); err != nil {
			return domain.RetrievedDocumentChunk{}, err
		}
	}
	if item.Document.Metadata == nil {
		item.Document.Metadata = map[string]any{}
	}
	if item.Chunk.Metadata == nil {
		item.Chunk.Metadata = map[string]any{}
	}
	item.Chunk.Document = item.Document
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
		memory_enabled boolean NOT NULL DEFAULT true,
		retrieval_enabled boolean NOT NULL DEFAULT true,
		executor text NOT NULL DEFAULT 'native',
		deleted_at timestamptz,
		created_at timestamptz NOT NULL,
		updated_at timestamptz NOT NULL
	)`,
	`ALTER TABLE agents ADD COLUMN IF NOT EXISTS memory_enabled boolean NOT NULL DEFAULT true`,
	`ALTER TABLE agents ADD COLUMN IF NOT EXISTS retrieval_enabled boolean NOT NULL DEFAULT true`,
	`ALTER TABLE agents ADD COLUMN IF NOT EXISTS executor text NOT NULL DEFAULT 'native'`,
	`ALTER TABLE agents ADD COLUMN IF NOT EXISTS deleted_at timestamptz`,
	`DO $$
	BEGIN
		IF EXISTS (
			SELECT 1
			FROM information_schema.columns
			WHERE table_name = 'agents' AND column_name = 'archived'
		) THEN
			UPDATE agents SET deleted_at = COALESCE(deleted_at, updated_at) WHERE archived = true;
		END IF;
	END $$`,
	`ALTER TABLE agents DROP COLUMN IF EXISTS archived`,
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
		heartbeat_at timestamptz,
		completed_at timestamptz,
		created_at timestamptz NOT NULL,
		updated_at timestamptz NOT NULL
	)`,
	`ALTER TABLE runs ADD COLUMN IF NOT EXISTS heartbeat_at timestamptz`,
	`ALTER TABLE runs ADD COLUMN IF NOT EXISTS runtime_snapshot jsonb`,
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
	`CREATE TABLE IF NOT EXISTS run_events (
		id text PRIMARY KEY,
		run_id text NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
		conversation_id text,
		stage_id text,
		turn_id text,
		parent_event_id text,
		type text NOT NULL,
		schema_version integer NOT NULL,
		sequence bigint NOT NULL,
		payload jsonb NOT NULL DEFAULT '{}'::jsonb,
		timestamp timestamptz NOT NULL,
		UNIQUE(run_id, sequence)
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
	`CREATE TABLE IF NOT EXISTS documents (
		id text PRIMARY KEY,
		workspace_id text,
		title text NOT NULL,
		source_type text NOT NULL,
		source_uri text,
		mime_type text,
		content text NOT NULL,
		metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
		created_at timestamptz NOT NULL,
		updated_at timestamptz NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS document_chunks (
		id text PRIMARY KEY,
		document_id text NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
		chunk_index integer NOT NULL,
		content text NOT NULL,
		token_count integer NOT NULL DEFAULT 0,
		metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
		created_at timestamptz NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS document_chunk_embeddings (
		chunk_id text PRIMARY KEY REFERENCES document_chunks(id) ON DELETE CASCADE,
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
	`CREATE INDEX IF NOT EXISTS idx_run_events_run_sequence ON run_events(run_id, sequence ASC)`,
	`CREATE INDEX IF NOT EXISTS idx_memories_scope_created ON memories(workspace_id, user_id, project_id, created_at DESC)`,
	`CREATE INDEX IF NOT EXISTS idx_memory_embeddings_vector ON memory_embeddings USING hnsw (embedding vector_cosine_ops)`,
	`CREATE INDEX IF NOT EXISTS idx_documents_workspace_created ON documents(workspace_id, created_at DESC)`,
	`CREATE INDEX IF NOT EXISTS idx_documents_metadata ON documents USING gin(metadata)`,
	`CREATE INDEX IF NOT EXISTS idx_document_chunks_document ON document_chunks(document_id, chunk_index)`,
	`CREATE INDEX IF NOT EXISTS idx_document_chunks_metadata ON document_chunks USING gin(metadata)`,
	`CREATE INDEX IF NOT EXISTS idx_document_chunk_embeddings_vector ON document_chunk_embeddings USING hnsw (embedding vector_cosine_ops)`,
}

var _ Store = (*PostgresStore)(nil)
