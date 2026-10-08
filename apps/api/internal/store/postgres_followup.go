package store

import (
	"context"
	"database/sql"
	"errors"

	"agentflow-platform/apps/api/internal/domain"
)

// The caller holds the existing Conversation single-writer. This transaction
// couples receipt, message and fresh Run so retries cannot create duplicate Runs.
func (s *PostgresStore) applyFollowup(ctx context.Context, tx *sql.Tx, id, owner string, allowStopped bool, run domain.Run) error {
	if err := lockInbox(ctx, tx, owner, run.WorkspaceID, run.ConversationID); err != nil {
		return err
	}
	var oldest string
	err := tx.QueryRowContext(ctx, `SELECT id FROM conversation_inputs WHERE conversation_id=$1 AND request->>'kind'='follow_up' AND status='queued' AND expires_at>NOW() ORDER BY created_at,id LIMIT 1`, run.ConversationID).Scan(&oldest)
	if errors.Is(err, sql.ErrNoRows) || err == nil && oldest != id {
		return ErrInputConflict
	}
	if err != nil {
		return err
	}
	var status string
	if err = tx.QueryRowContext(ctx, `SELECT status FROM runs WHERE conversation_id=$1 ORDER BY created_at DESC,id DESC LIMIT 1 FOR UPDATE`, run.ConversationID).Scan(&status); err != nil {
		return err
	}
	if status != "completed" && !(allowStopped && (status == "failed" || status == "canceled")) {
		return ErrInputConflict
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO messages(id,workspace_id,conversation_id,role,content,created_at)
 SELECT id,workspace_id,conversation_id,'user',request->>'content',NOW() FROM conversation_inputs WHERE id=$1 AND owner_user_id=$2 AND status='queued'`, id, owner)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrInputConflict
	}
	return nil
}
