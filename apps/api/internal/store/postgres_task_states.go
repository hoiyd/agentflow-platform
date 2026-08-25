package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/domain"
)

func (s *PostgresStore) GetTaskState(conversationID string) (domain.TaskState, bool, error) {
	var stateJSON []byte
	err := s.db.QueryRow(`
		SELECT state FROM task_state_revisions
		WHERE conversation_id = $1 ORDER BY version DESC LIMIT 1`, conversationID).Scan(&stateJSON)
	if errors.Is(err, sql.ErrNoRows) {
		if _, ok, conversationErr := s.GetConversation(conversationID); conversationErr != nil {
			return domain.TaskState{}, false, conversationErr
		} else if !ok {
			return domain.TaskState{}, false, ErrNotFound("conversation")
		}
		return domain.TaskState{}, false, nil
	}
	if err != nil {
		return domain.TaskState{}, false, err
	}
	var state domain.TaskState
	if err := json.Unmarshal(stateJSON, &state); err != nil {
		return domain.TaskState{}, false, err
	}
	return state, true, nil
}

func (s *PostgresStore) GetTaskStateRevision(conversationID string, version int64) (domain.TaskStateRevision, bool, error) {
	revision, err := scanTaskStateRevision(s.db.QueryRow(`
		SELECT id, workspace_id, conversation_id, version, previous_version, patch, state, source, created_at
		FROM task_state_revisions WHERE conversation_id = $1 AND version = $2`, conversationID, version))
	if errors.Is(err, sql.ErrNoRows) {
		if _, ok, conversationErr := s.GetConversation(conversationID); conversationErr != nil {
			return domain.TaskStateRevision{}, false, conversationErr
		} else if !ok {
			return domain.TaskStateRevision{}, false, ErrNotFound("conversation")
		}
		return domain.TaskStateRevision{}, false, nil
	}
	return revision, err == nil, err
}

func (s *PostgresStore) ListTaskStateRevisions(conversationID string) ([]domain.TaskStateRevision, error) {
	if _, ok, err := s.GetConversation(conversationID); err != nil {
		return nil, err
	} else if !ok {
		return nil, ErrNotFound("conversation")
	}
	rows, err := s.db.Query(`
		SELECT id, workspace_id, conversation_id, version, previous_version, patch, state, source, created_at
		FROM task_state_revisions WHERE conversation_id = $1 ORDER BY version ASC`, conversationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]domain.TaskStateRevision, 0)
	for rows.Next() {
		item, err := scanTaskStateRevision(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *PostgresStore) ApplyTaskStatePatch(conversationID string, patch domain.TaskStatePatch, source domain.TaskStateSource) (domain.TaskStateRevision, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return domain.TaskStateRevision{}, err
	}
	defer tx.Rollback()
	var workspaceID string
	if err := tx.QueryRow(`SELECT workspace_id FROM conversations WHERE id = $1 FOR UPDATE`, conversationID).Scan(&workspaceID); errors.Is(err, sql.ErrNoRows) {
		return domain.TaskStateRevision{}, ErrNotFound("conversation")
	} else if err != nil {
		return domain.TaskStateRevision{}, err
	}
	if err := validatePostgresTaskStateSource(tx, conversationID, source); err != nil {
		return domain.TaskStateRevision{}, err
	}
	current := domain.EmptyTaskState(workspaceID, conversationID)
	var stateJSON []byte
	err = tx.QueryRow(`SELECT state FROM task_state_revisions WHERE conversation_id = $1 ORDER BY version DESC LIMIT 1`, conversationID).Scan(&stateJSON)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return domain.TaskStateRevision{}, err
	}
	if err == nil {
		if err := json.Unmarshal(stateJSON, &current); err != nil {
			return domain.TaskStateRevision{}, err
		}
	}
	if patch.ExpectedVersion != current.Version {
		return domain.TaskStateRevision{}, &TaskStateVersionConflict{Expected: patch.ExpectedVersion, Actual: current.Version}
	}
	now := time.Now().UTC()
	next, err := domain.ApplyTaskStatePatch(current, patch, now)
	if err != nil {
		return domain.TaskStateRevision{}, classifyTaskStateValidation(err)
	}
	source = normalizeTaskStateSource(source)
	patchJSON, err := json.Marshal(patch)
	if err != nil {
		return domain.TaskStateRevision{}, err
	}
	stateJSON, err = json.Marshal(next)
	if err != nil {
		return domain.TaskStateRevision{}, err
	}
	sourceJSON, err := json.Marshal(source)
	if err != nil {
		return domain.TaskStateRevision{}, err
	}
	revision := domain.TaskStateRevision{
		ID: newID("tsr"), WorkspaceID: normalizeWorkspaceID(workspaceID), ConversationID: conversationID,
		Version: next.Version, PreviousVersion: current.Version, Patch: patch, State: next,
		Source: source, CreatedAt: now,
	}
	if _, err := tx.Exec(`
		INSERT INTO task_state_revisions
			(id, workspace_id, conversation_id, version, previous_version, patch, state, source, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		revision.ID, revision.WorkspaceID, revision.ConversationID, revision.Version,
		revision.PreviousVersion, patchJSON, stateJSON, sourceJSON, revision.CreatedAt); err != nil {
		return domain.TaskStateRevision{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.TaskStateRevision{}, err
	}
	return revision, nil
}

type taskStateRevisionScanner interface {
	Scan(...any) error
}

func scanTaskStateRevision(scanner taskStateRevisionScanner) (domain.TaskStateRevision, error) {
	var revision domain.TaskStateRevision
	var patchJSON, stateJSON, sourceJSON []byte
	if err := scanner.Scan(
		&revision.ID, &revision.WorkspaceID, &revision.ConversationID, &revision.Version,
		&revision.PreviousVersion, &patchJSON, &stateJSON, &sourceJSON, &revision.CreatedAt,
	); err != nil {
		return domain.TaskStateRevision{}, err
	}
	if err := json.Unmarshal(patchJSON, &revision.Patch); err != nil {
		return domain.TaskStateRevision{}, err
	}
	if err := json.Unmarshal(stateJSON, &revision.State); err != nil {
		return domain.TaskStateRevision{}, err
	}
	if err := json.Unmarshal(sourceJSON, &revision.Source); err != nil {
		return domain.TaskStateRevision{}, err
	}
	return revision, nil
}

func validatePostgresTaskStateSource(tx *sql.Tx, conversationID string, source domain.TaskStateSource) error {
	if runID := strings.TrimSpace(source.RunID); runID != "" {
		var valid bool
		if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM runs WHERE id=$1 AND conversation_id=$2)`, runID, conversationID).Scan(&valid); err != nil {
			return err
		}
		if !valid {
			return classifyTaskStateValidation(errors.New("task state source run does not belong to conversation"))
		}
	}
	if messageID := strings.TrimSpace(source.SourceMessageID); messageID != "" {
		var valid bool
		if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM messages WHERE id=$1 AND conversation_id=$2)`, messageID, conversationID).Scan(&valid); err != nil {
			return err
		}
		if !valid {
			return classifyTaskStateValidation(errors.New("task state source message does not belong to conversation"))
		}
	}
	return nil
}
