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
	var oldest, executionConstraint string
	err := tx.QueryRowContext(ctx, `SELECT i.id,COALESCE(r.runtime_snapshot->>'execution_constraint','')
 FROM conversation_inputs i JOIN runs r ON r.id=i.run_id
 WHERE i.conversation_id=$1 AND i.request->>'kind'='follow_up' AND i.status='queued' AND i.expires_at>NOW()
 ORDER BY i.created_at,i.id LIMIT 1`, run.ConversationID).Scan(&oldest, &executionConstraint)
	if errors.Is(err, sql.ErrNoRows) || err == nil && oldest != id {
		return ErrInputConflict
	}
	if err != nil {
		return err
	}
	if err = ValidateFollowupIntent(executionConstraint); err != nil {
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
