package store

import (
	"context"
	"time"

	"agentflow-platform/apps/api/internal/approval"
	"agentflow-platform/apps/api/internal/domain"
)

func (s *PostgresStore) DecideToolApproval(ctx context.Context, owner, workspace, run, id string, input domain.ToolApprovalDecision) (approval.Mutation, error) {
	if input.Version < 1 || len(input.IdempotencyKey) == 0 || len(input.IdempotencyKey) > 128 || input.Decision != "approve" && input.Decision != "reject" && input.Decision != "revoke" {
		return approval.Mutation{}, approval.ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return approval.Mutation{}, err
	}
	defer tx.Rollback()
	m, status, err := lockToolApproval(ctx, tx, owner, workspace, run, id)
	if err != nil {
		return m, err
	}
	want := map[string]string{"approve": "approved", "reject": "rejected", "revoke": "revoked"}[input.Decision]
	if m.Version != input.Version {
		return m, approval.ErrConflict
	}
	if m.Status == want || want == "approved" && (m.Status == "claimed" || m.Status == "consumed") {
		var same bool
		err = tx.QueryRowContext(ctx, `SELECT decision_key=$2 FROM tool_approvals WHERE id=$1`, id, input.IdempotencyKey).Scan(&same)
		if err != nil {
			return m, err
		}
		if same {
			return m, nil
		}
		return m, approval.ErrConflict
	}
	if status != "waiting_for_user" || !m.ExpiresAt.After(time.Now()) || m.Status != "pending" && !(m.Status == "approved" && input.Decision == "revoke") {
		return m, approval.ErrConflict
	}
	now := time.Now().UTC()
	m.Status, m.DecidedAt = want, &now
	if err = saveToolApproval(ctx, tx, m); err != nil {
		return m, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE tool_approvals SET decision_key=$2 WHERE id=$1`, id, input.IdempotencyKey); err != nil {
		return m, err
	}
	if err = approvalEvent(ctx, tx, &m, domain.EventToolApprovalDecided); err != nil {
		return m, err
	}
	if want != "approved" {
		if err = clarificationRunStatus(ctx, tx, run, "canceled"); err != nil {
			return m, err
		}
		if err = approvalEvent(ctx, tx, &m, domain.EventRunCanceled); err != nil {
			return m, err
		}
	}
	return m, tx.Commit()
}

// Claim transfers the durable continuation to one admitted execution epoch.
// After a crash, only failed_recoverable may rotate it; a stale worker's token
// cannot consume the new claim even if its original request is still in memory.
func (s *PostgresStore) ClaimToolApproval(ctx context.Context, owner, workspace, run, id string) (approval.Mutation, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return approval.Mutation{}, err
	}
	defer tx.Rollback()
	m, status, err := lockToolApproval(ctx, tx, owner, workspace, run, id)
	if err != nil {
		return m, err
	}
	if !m.ExpiresAt.After(time.Now()) || !(status == "waiting_for_user" && m.Status == "approved" || status == "failed_recoverable" && (m.Status == "claimed" || m.Status == "consumed")) {
		return m, approval.ErrConflict
	}
	var latest string
	if err = tx.QueryRowContext(ctx, `SELECT id FROM tool_approvals WHERE run_id=$1 ORDER BY (record->>'created_at')::timestamptz DESC,id DESC LIMIT 1`, run).Scan(&latest); err != nil {
		return m, err
	}
	if latest != id {
		return m, approval.ErrConflict
	}
	var bound bool
	if err = tx.QueryRowContext(ctx, `SELECT payload->>'kind'='tool_approval' AND payload->'approval'->>'id'=$2 FROM run_events WHERE run_id=$1 AND type='run.waiting_for_user' ORDER BY sequence DESC LIMIT 1`, run, id).Scan(&bound); err != nil {
		return m, err
	}
	if !bound {
		return m, approval.ErrConflict
	}
	var uncertain bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM tool_effects WHERE run_id=$1 AND status IN('prepared','executing','needs_reconciliation','reconciling'))`, run).Scan(&uncertain); err != nil {
		return m, err
	}
	if uncertain {
		return m, approval.ErrConflict
	}
	m.Status, m.Claim = "claimed", NewID("claim")
	if err = saveToolApproval(ctx, tx, m); err != nil {
		return m, err
	}
	if err = clarificationRunStatus(ctx, tx, run, "running"); err != nil {
		return m, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE tool_approvals SET execution_started_at=(SELECT execution_started_at FROM runs WHERE id=$2) WHERE id=$1`, id, run); err != nil {
		return m, err
	}
	if err = approvalEvent(ctx, tx, &m, domain.EventRunResumed); err != nil {
		return m, err
	}
	if status == "failed_recoverable" && m.StageID != "" {
		var stage string
		if err = tx.QueryRowContext(ctx, `SELECT status FROM collaboration_steps WHERE id=$1 AND run_id=$2 FOR UPDATE`, m.StageID, run).Scan(&stage); err != nil {
			return m, err
		}
		if stage == "failed" {
			if _, err = tx.ExecContext(ctx, `UPDATE collaboration_steps SET status='running',error='',updated_at=NOW() WHERE id=$1`, m.StageID); err != nil {
				return m, err
			}
			// Reopen only the existing interrupted Stage, never create a replacement.
			if err = approvalEvent(ctx, tx, &m, domain.EventStageStarted); err != nil {
				return m, err
			}
			changed, updateErr := tx.ExecContext(ctx, `UPDATE stage_checkpoints SET status='executing',error='',updated_at=NOW() WHERE stage_id=$1 AND run_id=$2 AND runtime_snapshot_hash=$3`, m.StageID, run, m.SnapshotHash)
			if updateErr != nil {
				return m, updateErr
			}
			count, countErr := changed.RowsAffected()
			if countErr != nil {
				return m, countErr
			}
			if count != 1 {
				return m, approval.ErrConflict
			}
		}
	}
	return m, tx.Commit()
}

func (s *PostgresStore) ConsumeToolApproval(ctx context.Context, owner, workspace, id, claim, bindingHash string) (approval.Mutation, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return approval.Mutation{}, err
	}
	defer tx.Rollback()
	var run string
	if err = tx.QueryRowContext(ctx, `SELECT run_id FROM tool_approvals WHERE id=$1`, id).Scan(&run); err != nil {
		return approval.Mutation{}, err
	}
	m, status, err := lockToolApproval(ctx, tx, owner, workspace, run, id)
	if err != nil {
		return m, err
	}
	var current bool
	if err = tx.QueryRowContext(ctx, `SELECT a.execution_started_at=r.execution_started_at FROM tool_approvals a JOIN runs r ON r.id=a.run_id WHERE a.id=$1`, id).Scan(&current); err != nil {
		return m, err
	}
	if status != "running" || m.Status != "claimed" || m.Claim != claim || claim == "" || !current || !m.ExpiresAt.After(time.Now()) || approval.BindingHash(m.ToolApproval) != bindingHash {
		return m, approval.ErrConflict
	}
	m.Status = "consumed"
	if err = saveToolApproval(ctx, tx, m); err != nil {
		return m, err
	}
	if err = approvalEvent(ctx, tx, &m, domain.EventToolApprovalConsumed); err != nil {
		return m, err
	}
	return m, tx.Commit()
}
