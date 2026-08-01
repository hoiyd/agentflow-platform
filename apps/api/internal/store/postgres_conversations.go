package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"agentflow-platform/apps/api/internal/domain"
)

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
