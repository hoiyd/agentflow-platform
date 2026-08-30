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
		SELECT id, COALESCE(workspace_id, 'default_workspace'), title, created_at, updated_at
		FROM conversations
		ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []domain.Conversation{}
	for rows.Next() {
		var item domain.Conversation
		if err := rows.Scan(&item.ID, &item.WorkspaceID, &item.Title, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *PostgresStore) ListConversationsByWorkspace(workspaceID string) ([]domain.Conversation, error) {
	rows, err := s.db.Query(`SELECT id, workspace_id, title, created_at, updated_at FROM conversations WHERE workspace_id = $1 ORDER BY updated_at DESC`, normalizeWorkspaceID(workspaceID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []domain.Conversation{}
	for rows.Next() {
		var item domain.Conversation
		if err := rows.Scan(&item.ID, &item.WorkspaceID, &item.Title, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *PostgresStore) CreateConversation(title string) (domain.Conversation, error) {
	return s.CreateConversationInWorkspace(domain.DefaultWorkspaceID, title)
}

func (s *PostgresStore) CreateConversationInWorkspace(workspaceID string, title string) (domain.Conversation, error) {
	now := time.Now().UTC()
	conversation := domain.Conversation{
		ID: newID("conv"), WorkspaceID: normalizeWorkspaceID(workspaceID), Title: normalizeTitle(title), CreatedAt: now, UpdatedAt: now,
	}
	_, err := s.db.Exec(`
		INSERT INTO conversations (id, workspace_id, title, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5)`,
		conversation.ID, conversation.WorkspaceID, conversation.Title, conversation.CreatedAt, conversation.UpdatedAt)
	return conversation, err
}

func (s *PostgresStore) GetConversation(id string) (domain.Conversation, bool, error) {
	var item domain.Conversation
	err := s.db.QueryRow(`
		SELECT id, COALESCE(workspace_id, 'default_workspace'), title, created_at, updated_at
		FROM conversations
		WHERE id = $1`, id).Scan(&item.ID, &item.WorkspaceID, &item.Title, &item.CreatedAt, &item.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Conversation{}, false, nil
	}
	if err != nil {
		return domain.Conversation{}, false, err
	}
	return item, true, nil
}

func (s *PostgresStore) GetConversationInWorkspace(workspaceID string, id string) (domain.Conversation, bool, error) {
	var item domain.Conversation
	err := s.db.QueryRow(`SELECT id, workspace_id, title, created_at, updated_at FROM conversations WHERE id = $1 AND workspace_id = $2`, id, normalizeWorkspaceID(workspaceID)).Scan(&item.ID, &item.WorkspaceID, &item.Title, &item.CreatedAt, &item.UpdatedAt)
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

func (s *PostgresStore) DeleteConversationInWorkspace(workspaceID string, id string) error {
	result, err := s.db.Exec(`DELETE FROM conversations WHERE id = $1 AND workspace_id = $2`, id, normalizeWorkspaceID(workspaceID))
	if err != nil {
		return err
	}
	if affected, affectedErr := result.RowsAffected(); affectedErr == nil && affected == 0 {
		return ErrNotFound("conversation")
	}
	return nil
}

func (s *PostgresStore) ListMessages(conversationID string) ([]domain.Message, error) {
	rows, err := s.db.Query(`
		SELECT id, COALESCE(workspace_id, 'default_workspace'), conversation_id, role, content, citations, created_at
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
		if err := rows.Scan(&item.ID, &item.WorkspaceID, &item.ConversationID, &item.Role, &item.Content, &citationsJSON, &item.CreatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(citationsJSON, &item.Citations); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *PostgresStore) ListMessagesInWorkspace(workspaceID string, conversationID string) ([]domain.Message, error) {
	rows, err := s.db.Query(`
		SELECT m.id, m.workspace_id, m.conversation_id, m.role, m.content, m.citations, m.created_at
		FROM messages m
		JOIN conversations c ON c.id = m.conversation_id
		WHERE m.conversation_id = $1 AND m.workspace_id = $2 AND c.workspace_id = $2
		ORDER BY m.created_at ASC`, conversationID, normalizeWorkspaceID(workspaceID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []domain.Message{}
	for rows.Next() {
		var item domain.Message
		var citationsJSON []byte
		if err := rows.Scan(&item.ID, &item.WorkspaceID, &item.ConversationID, &item.Role, &item.Content, &citationsJSON, &item.CreatedAt); err != nil {
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
	conversation, ok, err := s.GetConversation(conversationID)
	if err != nil {
		return domain.Message{}, err
	}
	if !ok {
		return domain.Message{}, errors.New("conversation not found")
	}
	now := time.Now().UTC()
	message := domain.Message{
		ID:             newID("msg"),
		WorkspaceID:    conversation.WorkspaceID,
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
		INSERT INTO messages (id, workspace_id, conversation_id, role, content, citations, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		message.ID, message.WorkspaceID, message.ConversationID, message.Role, message.Content, citationsJSON, message.CreatedAt); err != nil {
		return domain.Message{}, err
	}
	if _, err := tx.Exec(`UPDATE conversations SET updated_at = $1 WHERE id = $2`, now, conversationID); err != nil {
		return domain.Message{}, err
	}
	return message, tx.Commit()
}

func (s *PostgresStore) AddMessageInWorkspace(workspaceID string, conversationID string, role string, content string) (domain.Message, error) {
	return s.AddMessageWithCitationsInWorkspace(workspaceID, conversationID, role, content, nil)
}

func (s *PostgresStore) AddMessageWithCitationsInWorkspace(workspaceID string, conversationID string, role string, content string, citations []domain.RAGCitation) (domain.Message, error) {
	if _, ok, err := s.GetConversationInWorkspace(workspaceID, conversationID); err != nil {
		return domain.Message{}, err
	} else if !ok {
		return domain.Message{}, ErrNotFound("conversation")
	}
	return s.AddMessageWithCitations(conversationID, role, content, citations)
}

func (s *PostgresStore) CommitContextCompaction(compaction domain.ContextCompaction, completion domain.RunEvent) (domain.ContextCompaction, domain.RunEvent, error) {
	if completion.Type != domain.EventCompactionCompleted || completion.RunID != compaction.RunID || completion.ConversationID != compaction.ConversationID {
		return domain.ContextCompaction{}, domain.RunEvent{}, errors.New("compaction completion event does not match compaction")
	}
	var err error
	compaction, err = s.preparePostgresContextCompaction(compaction, domain.ContextCompactionCompleted)
	if err != nil {
		return domain.ContextCompaction{}, domain.RunEvent{}, err
	}
	completion, payload, err := preparePostgresRunEvent(completion, compaction.CreatedAt)
	if err != nil {
		return domain.ContextCompaction{}, domain.RunEvent{}, err
	}
	sourceMessageIDs, err := json.Marshal(compaction.SourceMessageIDs)
	if err != nil {
		return domain.ContextCompaction{}, domain.RunEvent{}, err
	}
	sourceEventIDs, err := json.Marshal(compaction.SourceEventIDs)
	if err != nil {
		return domain.ContextCompaction{}, domain.RunEvent{}, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return domain.ContextCompaction{}, domain.RunEvent{}, err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`SELECT pg_advisory_xact_lock(hashtext($1))`, "context-compaction:"+compaction.ConversationID); err != nil {
		return domain.ContextCompaction{}, domain.RunEvent{}, err
	}
	result, err := tx.Exec(`
		INSERT INTO context_compactions (
			id, conversation_id, run_id, trigger, status, generation,
			previous_compaction_id, replacement_summary_id, summary, source_message_ids,
			source_event_ids, shadowed_first_message_id, shadowed_last_message_id,
			shadowed_message_count, source_hash, before_tokens, after_tokens,
			target_summary_tokens, reduction_ratio, consecutive_low_yield,
			summary_model, algorithm_version, surface_replaced_at, created_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24)
		ON CONFLICT (conversation_id, source_hash) DO NOTHING`,
		compaction.ID, compaction.ConversationID, compaction.RunID, compaction.Trigger,
		compaction.Status, compaction.Generation, compaction.PreviousCompactionID,
		compaction.ReplacementSummaryID, compaction.Summary, sourceMessageIDs, sourceEventIDs,
		compaction.ShadowedMessageRange.FirstMessageID, compaction.ShadowedMessageRange.LastMessageID,
		compaction.ShadowedMessageRange.MessageCount, compaction.SourceHash, compaction.BeforeTokens,
		compaction.AfterTokens, compaction.TargetSummaryTokens, compaction.ReductionRatio,
		compaction.ConsecutiveLowYield, compaction.SummaryModel, compaction.AlgorithmVersion,
		compaction.SurfaceReplacedAt, compaction.CreatedAt)
	if err != nil {
		return domain.ContextCompaction{}, domain.RunEvent{}, err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		existing, getErr := scanContextCompaction(tx.QueryRow(contextCompactionSelect+` WHERE conversation_id = $1 AND source_hash = $2`, compaction.ConversationID, compaction.SourceHash))
		if getErr != nil {
			return domain.ContextCompaction{}, domain.RunEvent{}, getErr
		}
		if err := tx.Commit(); err != nil {
			return domain.ContextCompaction{}, domain.RunEvent{}, err
		}
		return existing, domain.RunEvent{}, nil
	}
	if _, err = tx.Exec(`SELECT pg_advisory_xact_lock(hashtext($1))`, completion.RunID); err != nil {
		return domain.ContextCompaction{}, domain.RunEvent{}, err
	}
	err = tx.QueryRow(`
		INSERT INTO run_events (id, run_id, conversation_id, stage_id, turn_id, parent_event_id, type, schema_version, sequence, payload, timestamp)
		VALUES ($1,$2,NULLIF($3,''),NULLIF($4,''),NULLIF($5,''),NULLIF($6,''),$7,$8,
			(SELECT COALESCE(MAX(sequence),0)+1 FROM run_events WHERE run_id=$2),$9,$10)
		RETURNING sequence`, completion.ID, completion.RunID, completion.ConversationID, completion.StageID,
		completion.TurnID, completion.ParentEventID, string(completion.Type), completion.SchemaVersion,
		payload, completion.Timestamp).Scan(&completion.Sequence)
	if err != nil {
		return domain.ContextCompaction{}, domain.RunEvent{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.ContextCompaction{}, domain.RunEvent{}, err
	}
	return compaction, completion, nil
}

func (s *PostgresStore) GetLatestContextCompaction(conversationID string) (domain.ContextCompaction, bool, error) {
	row := s.db.QueryRow(contextCompactionSelect+`
		WHERE conversation_id = $1 AND status = 'completed'
		ORDER BY generation DESC, created_at DESC, id DESC LIMIT 1`, conversationID)
	item, err := scanContextCompaction(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ContextCompaction{}, false, nil
	}
	return item, err == nil, err
}

type contextCompactionScanner interface {
	Scan(...any) error
}

const contextCompactionSelect = `
	SELECT id, conversation_id, run_id, trigger, status, generation,
		previous_compaction_id, replacement_summary_id, summary, source_message_ids,
		source_event_ids, shadowed_first_message_id, shadowed_last_message_id,
		shadowed_message_count, source_hash, before_tokens, after_tokens,
		target_summary_tokens, reduction_ratio, consecutive_low_yield,
		summary_model, algorithm_version, surface_replaced_at, created_at
	FROM context_compactions`

func scanContextCompaction(row contextCompactionScanner) (domain.ContextCompaction, error) {
	var item domain.ContextCompaction
	var sourceMessageIDs, sourceEventIDs []byte
	err := row.Scan(&item.ID, &item.ConversationID, &item.RunID, &item.Trigger, &item.Status,
		&item.Generation, &item.PreviousCompactionID, &item.ReplacementSummaryID, &item.Summary,
		&sourceMessageIDs, &sourceEventIDs, &item.ShadowedMessageRange.FirstMessageID,
		&item.ShadowedMessageRange.LastMessageID, &item.ShadowedMessageRange.MessageCount,
		&item.SourceHash, &item.BeforeTokens, &item.AfterTokens, &item.TargetSummaryTokens,
		&item.ReductionRatio, &item.ConsecutiveLowYield, &item.SummaryModel,
		&item.AlgorithmVersion, &item.SurfaceReplacedAt, &item.CreatedAt)
	if err != nil {
		return domain.ContextCompaction{}, err
	}
	if err := json.Unmarshal(sourceMessageIDs, &item.SourceMessageIDs); err != nil {
		return domain.ContextCompaction{}, err
	}
	if err := json.Unmarshal(sourceEventIDs, &item.SourceEventIDs); err != nil {
		return domain.ContextCompaction{}, err
	}
	return item, nil
}

func (s *PostgresStore) getContextCompactionByHash(conversationID, sourceHash string) (domain.ContextCompaction, error) {
	return scanContextCompaction(s.db.QueryRow(contextCompactionSelect+` WHERE conversation_id = $1 AND source_hash = $2`, conversationID, sourceHash))
}

func (s *PostgresStore) preparePostgresContextCompaction(compaction domain.ContextCompaction, status domain.ContextCompactionStatus) (domain.ContextCompaction, error) {
	if compaction.ID == "" {
		compaction.ID = newID("cmp")
	}
	if compaction.Generation <= 0 {
		if err := s.db.QueryRow(`SELECT COALESCE(MAX(generation), 0) + 1 FROM context_compactions WHERE conversation_id = $1`, compaction.ConversationID).Scan(&compaction.Generation); err != nil {
			return domain.ContextCompaction{}, err
		}
	}
	if compaction.ReplacementSummaryID == "" {
		compaction.ReplacementSummaryID = "summary:" + compaction.ID
	}
	if compaction.CreatedAt.IsZero() {
		compaction.CreatedAt = time.Now().UTC()
	}
	compaction.Status = status
	if status == domain.ContextCompactionCompleted && compaction.SurfaceReplacedAt == nil {
		replacedAt := compaction.CreatedAt
		compaction.SurfaceReplacedAt = &replacedAt
	}
	return compaction, nil
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

func (s *PostgresStore) UpdateConversationTitleInWorkspace(workspaceID string, id string, title string) error {
	result, err := s.db.Exec(`UPDATE conversations SET title = $1, updated_at = $2 WHERE id = $3 AND workspace_id = $4`, normalizeTitle(title), time.Now().UTC(), id, normalizeWorkspaceID(workspaceID))
	if err != nil {
		return err
	}
	if affected, affectedErr := result.RowsAffected(); affectedErr == nil && affected == 0 {
		return ErrNotFound("conversation")
	}
	return nil
}
