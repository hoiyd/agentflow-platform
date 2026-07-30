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
		SELECT id, conversation_id, role, content, citations, created_at
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
		var citationsJSON []byte
		if err := rows.Scan(&item.ID, &item.ConversationID, &item.Role, &item.Content, &citationsJSON, &item.CreatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(citationsJSON, &item.Citations); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *PostgresStore) AddMessage(conversationID string, role string, content string) (domain.Message, error) {
	return s.AddMessageWithCitations(conversationID, role, content, nil)
}

func (s *PostgresStore) AddMessageWithCitations(conversationID string, role string, content string, citations []domain.RAGCitation) (domain.Message, error) {
	now := time.Now().UTC()
	message := domain.Message{
		ID:             newID("msg"),
		ConversationID: conversationID,
		Role:           role,
		Content:        content,
		Citations:      cloneCitations(citations),
		CreatedAt:      now,
	}
	citationsJSON := []byte("[]")
	if len(message.Citations) > 0 {
		var err error
		citationsJSON, err = json.Marshal(message.Citations)
		if err != nil {
			return domain.Message{}, err
		}
	}
	tx, err := s.db.Begin()
	if err != nil {
		return domain.Message{}, err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`
		INSERT INTO messages (id, conversation_id, role, content, citations, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		message.ID, message.ConversationID, message.Role, message.Content, citationsJSON, message.CreatedAt); err != nil {
		return domain.Message{}, err
	}
	if _, err := tx.Exec(`UPDATE conversations SET updated_at = $1 WHERE id = $2`, now, conversationID); err != nil {
		return domain.Message{}, err
	}
	return message, tx.Commit()
}

func (s *PostgresStore) CreateContextCompaction(compaction domain.ContextCompaction) (domain.ContextCompaction, error) {
	if compaction.ID == "" {
		compaction.ID = newID("cmp")
	}
	if compaction.CreatedAt.IsZero() {
		compaction.CreatedAt = time.Now().UTC()
	}
	sourceIDs, err := json.Marshal(compaction.SourceMessageIDs)
	if err != nil {
		return domain.ContextCompaction{}, err
	}
	result, err := s.db.Exec(`
		INSERT INTO context_compactions (
			id, conversation_id, run_id, trigger, summary, source_message_ids,
			source_hash, before_tokens, after_tokens, summary_model,
			algorithm_version, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT (conversation_id, source_hash) DO NOTHING`,
		compaction.ID, compaction.ConversationID, compaction.RunID, compaction.Trigger,
		compaction.Summary, sourceIDs, compaction.SourceHash, compaction.BeforeTokens,
		compaction.AfterTokens, compaction.SummaryModel, compaction.AlgorithmVersion,
		compaction.CreatedAt)
	if err != nil {
		return domain.ContextCompaction{}, err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return s.getContextCompactionByHash(compaction.ConversationID, compaction.SourceHash)
	}
	return compaction, nil
}

func (s *PostgresStore) GetLatestContextCompaction(conversationID string) (domain.ContextCompaction, bool, error) {
	row := s.db.QueryRow(`
		SELECT id, conversation_id, run_id, trigger, summary, source_message_ids,
			source_hash, before_tokens, after_tokens, summary_model, algorithm_version, created_at
		FROM context_compactions WHERE conversation_id = $1
		ORDER BY created_at DESC, id DESC LIMIT 1`, conversationID)
	item, err := scanContextCompaction(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ContextCompaction{}, false, nil
	}
	return item, err == nil, err
}

type contextCompactionScanner interface {
	Scan(...any) error
}

func scanContextCompaction(row contextCompactionScanner) (domain.ContextCompaction, error) {
	var item domain.ContextCompaction
	var sourceIDs []byte
	err := row.Scan(&item.ID, &item.ConversationID, &item.RunID, &item.Trigger, &item.Summary,
		&sourceIDs, &item.SourceHash, &item.BeforeTokens, &item.AfterTokens, &item.SummaryModel,
		&item.AlgorithmVersion, &item.CreatedAt)
	if err != nil {
		return domain.ContextCompaction{}, err
	}
	if err := json.Unmarshal(sourceIDs, &item.SourceMessageIDs); err != nil {
		return domain.ContextCompaction{}, err
	}
	return item, nil
}

func (s *PostgresStore) getContextCompactionByHash(conversationID, sourceHash string) (domain.ContextCompaction, error) {
	return scanContextCompaction(s.db.QueryRow(`
		SELECT id, conversation_id, run_id, trigger, summary, source_message_ids,
			source_hash, before_tokens, after_tokens, summary_model, algorithm_version, created_at
		FROM context_compactions WHERE conversation_id = $1 AND source_hash = $2`, conversationID, sourceHash))
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

func (s *PostgresStore) CreateMemoryCandidate(candidate domain.MemoryCandidate) (domain.MemoryCandidate, bool, error) {
	var err error
	candidate, err = normalizeMemoryCandidate(candidate)
	if err != nil {
		return domain.MemoryCandidate{}, false, err
	}
	result, err := s.db.Exec(`
		INSERT INTO memory_candidates (
			id, conversation_id, run_id, source_message_id, source_role, kind, content,
			status, extraction_reason, policy_reason, confidence, created_at
		) VALUES ($1,NULLIF($2,''),NULLIF($3,''),$4,$5,$6,$7,$8,$9,$10,$11,$12)
		ON CONFLICT (id) DO NOTHING`,
		candidate.ID, candidate.ConversationID, candidate.RunID, candidate.SourceMessageID,
		candidate.SourceRole, candidate.Kind, candidate.Content, string(candidate.Status),
		candidate.ExtractionReason, candidate.PolicyReason, candidate.Confidence, candidate.CreatedAt)
	if err != nil {
		return domain.MemoryCandidate{}, false, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return domain.MemoryCandidate{}, false, err
	}
	if rows == 1 {
		return candidate, true, nil
	}
	existing, err := scanMemoryCandidate(s.db.QueryRow(`
		SELECT id,conversation_id,run_id,source_message_id,source_role,kind,content,status,
			extraction_reason,policy_reason,confidence,created_at
		FROM memory_candidates WHERE id=$1`, candidate.ID))
	return existing, false, err
}

func (s *PostgresStore) ListMemoryCandidates(conversationID string) ([]domain.MemoryCandidate, error) {
	query := `SELECT id,conversation_id,run_id,source_message_id,source_role,kind,content,status,
		extraction_reason,policy_reason,confidence,created_at FROM memory_candidates`
	args := []any{}
	if strings.TrimSpace(conversationID) != "" {
		query += " WHERE conversation_id=$1"
		args = append(args, conversationID)
	}
	query += " ORDER BY created_at,id"
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []domain.MemoryCandidate{}
	for rows.Next() {
		item, err := scanMemoryCandidate(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
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

func scanMemoryCandidate(row scanner) (domain.MemoryCandidate, error) {
	var item domain.MemoryCandidate
	var conversationID sql.NullString
	var runID sql.NullString
	var status string
	if err := row.Scan(
		&item.ID, &conversationID, &runID, &item.SourceMessageID, &item.SourceRole,
		&item.Kind, &item.Content, &status, &item.ExtractionReason, &item.PolicyReason, &item.Confidence, &item.CreatedAt,
	); err != nil {
		return domain.MemoryCandidate{}, err
	}
	if conversationID.Valid {
		item.ConversationID = conversationID.String
	}
	if runID.Valid {
		item.RunID = runID.String
	}
	item.Status = domain.MemoryCandidateStatus(status)
	return item, nil
}

func scanMemory(row scanner) (domain.Memory, error) {
	var item domain.Memory
	var metadataJSON []byte
	var workspaceID sql.NullString
	var userID sql.NullString
	var projectID sql.NullString
	var conversationID sql.NullString
	var runID sql.NullString
	var sourceMessageID sql.NullString
	if err := row.Scan(
		&item.ID, &workspaceID, &userID, &projectID, &conversationID, &runID,
		&sourceMessageID, &item.Kind, &item.Content, &metadataJSON, &item.CreatedAt, &item.UpdatedAt,
	); err != nil {
		return domain.Memory{}, err
	}
	if workspaceID.Valid {
		item.WorkspaceID = workspaceID.String
	}
	if userID.Valid {
		item.UserID = userID.String
	}
	if projectID.Valid {
		item.ProjectID = projectID.String
	}
	if conversationID.Valid {
		item.ConversationID = conversationID.String
	}
	if runID.Valid {
		item.RunID = runID.String
	}
	if sourceMessageID.Valid {
		item.SourceMessageID = sourceMessageID.String
	}
	if len(metadataJSON) > 0 {
		if err := json.Unmarshal(metadataJSON, &item.Metadata); err != nil {
			return domain.Memory{}, err
		}
	}
	if item.Metadata == nil {
		item.Metadata = map[string]any{}
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

var _ Store = (*PostgresStore)(nil)
