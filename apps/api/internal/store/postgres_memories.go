package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/domain"
)

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
	memory.WorkspaceID = normalizeWorkspaceID(memory.WorkspaceID)
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
	search.WorkspaceID = normalizeWorkspaceID(search.WorkspaceID)
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
	args = append(args, search.WorkspaceID)
	conditions = append(conditions, fmt.Sprintf("m.workspace_id = $%d", len(args)))
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
