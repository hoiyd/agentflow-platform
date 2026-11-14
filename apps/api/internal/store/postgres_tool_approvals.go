package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"agentflow-platform/apps/api/internal/approval"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/redaction"
)

const toolApprovalSchema = `CREATE TABLE IF NOT EXISTS tool_approvals (
 id text PRIMARY KEY, run_id text NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
 record jsonb NOT NULL, checkpoint jsonb NOT NULL, decision_key text NOT NULL DEFAULT '',
 claim text NOT NULL DEFAULT '', execution_started_at timestamptz
)`

func scanToolApproval(row scanner) (approval.Mutation, error) {
	var m approval.Mutation
	var data []byte
	err := row.Scan(&data, &m.Checkpoint, &m.Claim)
	if err == nil {
		err = json.Unmarshal(data, &m.ToolApproval)
	}
	return m, err
}

// Same lock order as Run transitions: run sequence, authenticated Inbox, Run row.
func lockToolApproval(ctx context.Context, tx *sql.Tx, owner, workspace, run, id string) (approval.Mutation, string, error) {
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, run); err != nil {
		return approval.Mutation{}, "", err
	}
	var conversation string
	if err := tx.QueryRowContext(ctx, `SELECT conversation_id FROM runs WHERE id=$1 AND workspace_id=$2`, run, workspace).Scan(&conversation); err != nil {
		return approval.Mutation{}, "", ErrNotFound("run")
	}
	if err := lockInbox(ctx, tx, owner, workspace, conversation); err != nil {
		return approval.Mutation{}, "", err
	}
	var status string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM runs WHERE id=$1 FOR UPDATE`, run).Scan(&status); err != nil {
		return approval.Mutation{}, "", err
	}
	m, err := scanToolApproval(tx.QueryRowContext(ctx, `SELECT record,checkpoint,claim FROM tool_approvals WHERE id=$1 AND run_id=$2 FOR UPDATE`, id, run))
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound("Tool approval")
	}
	if err == nil && (m.OwnerID != owner || m.WorkspaceID != workspace) {
		err = approval.ErrConflict
	}
	return m, status, err
}

func saveToolApproval(ctx context.Context, tx *sql.Tx, m approval.Mutation) error {
	data, err := json.Marshal(m.ToolApproval)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE tool_approvals SET record=$2,claim=$3 WHERE id=$1`, m.ID, data, m.Claim)
	return err
}

func approvalEvent(ctx context.Context, tx *sql.Tx, m *approval.Mutation, kind domain.RunEventType) error {
	payload := map[string]any{"kind": "tool_approval", "approval": m.ToolApproval}
	if kind == domain.EventRunWaitingForUser {
		payload["status"] = "waiting_for_user"
	}
	if kind == domain.EventRunResumed {
		payload["status"] = "running"
	}
	if kind == domain.EventRunCanceled {
		payload["status"] = "canceled"
	}
	if kind == domain.EventTurnCompleted {
		payload = map[string]any{"stop_reason": "waiting_for_user", "approval_id": m.ID}
	}
	if kind == domain.EventStageStarted {
		var role, agent, input string
		var iteration int
		if err := tx.QueryRowContext(ctx, `SELECT role,agent_id,input,iteration FROM collaboration_steps WHERE id=$1 AND run_id=$2`, m.StageID, m.RunID).Scan(&role, &agent, &input, &iteration); err != nil {
			return err
		}
		payload = map[string]any{"name": role, "agent_id": agent, "input": input, "iteration": iteration, "approval_id": m.ID}
	}
	data, _ := json.Marshal(payload)
	item := domain.RunEvent{ID: NewID("event"), RunID: m.RunID, ConversationID: m.ConversationID, StageID: m.StageID, TurnID: m.TurnID, Type: kind, SchemaVersion: 1, Timestamp: time.Now().UTC(), Payload: payload}
	err := tx.QueryRowContext(ctx, `INSERT INTO run_events(id,run_id,conversation_id,stage_id,turn_id,type,schema_version,sequence,payload,timestamp) VALUES($1,$2,$3,NULLIF($4,''),$5,$6,1,(SELECT COALESCE(MAX(sequence),0)+1 FROM run_events WHERE run_id=$2),$7,$8) RETURNING sequence`, item.ID, item.RunID, item.ConversationID, item.StageID, item.TurnID, kind, data, item.Timestamp).Scan(&item.Sequence)
	if err == nil {
		m.Events = append(m.Events, item)
	}
	return err
}

func (s *PostgresStore) SuspendToolApproval(ctx context.Context, record domain.ToolApproval, checkpoint json.RawMessage) (approval.Mutation, error) {
	m := approval.Mutation{ToolApproval: record}
	if record.RunID == "" || record.TurnID == "" || record.ToolCallID == "" || record.Tool != "file_patch" || record.ArgumentsHash == "" || record.DefinitionRevision == "" || record.SnapshotHash == "" || record.PolicyHash == "" || len(checkpoint) > approval.MaxCheckpointBytes || !json.Valid(checkpoint) {
		return m, approval.ErrInvalid
	}
	if err := redaction.ValidateValue(checkpoint); err != nil {
		return m, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return m, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, record.RunID); err != nil {
		return m, err
	}
	if err = tx.QueryRowContext(ctx, `SELECT w.owner_user_id,r.workspace_id FROM runs r JOIN workspaces w ON w.id=r.workspace_id WHERE r.id=$1 AND r.conversation_id=$2`, record.RunID, record.ConversationID).Scan(&m.OwnerID, &m.WorkspaceID); err != nil {
		return m, err
	}
	if err = lockInbox(ctx, tx, m.OwnerID, m.WorkspaceID, m.ConversationID); err != nil {
		return m, err
	}
	var status string
	if err = tx.QueryRowContext(ctx, `SELECT status FROM runs WHERE id=$1 FOR UPDATE`, record.RunID).Scan(&status); err != nil {
		return m, err
	}
	var real bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM run_events WHERE run_id=$1 AND turn_id=$2 AND COALESCE(stage_id,'')=$3 AND type='tool.failed' AND payload->>'tool_call_id'=$4 AND payload->>'tool_name'='file_patch' AND payload->>'error_code'='approval_required' AND payload->>'arguments_hash'=$5 AND payload->>'definition_revision'=$6)
 AND EXISTS(SELECT 1 FROM run_events WHERE run_id=$1 AND turn_id=$2 AND type='turn.started')
 AND ($3='' OR EXISTS(SELECT 1 FROM collaboration_steps WHERE id=$3 AND run_id=$1))`, record.RunID, record.TurnID, record.StageID, record.ToolCallID, record.ArgumentsHash, record.DefinitionRevision).Scan(&real); err != nil {
		return m, err
	}
	if status != "running" || !real {
		return m, approval.ErrConflict
	}
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM tool_approvals WHERE run_id=$1`, record.RunID).Scan(&count); err != nil {
		return m, err
	}
	if count >= 16 {
		return m, approval.ErrConflict
	}
	m.ID, m.Version, m.Status, m.CreatedAt = NewID("approval"), 1, "pending", time.Now().UTC()
	m.ExpiresAt = m.CreatedAt.Add(approval.Lifetime)
	data, _ := json.Marshal(m.ToolApproval)
	if _, err = tx.ExecContext(ctx, `INSERT INTO tool_approvals(id,run_id,record,checkpoint) VALUES($1,$2,$3,$4)`, m.ID, m.RunID, data, checkpoint); err != nil {
		return m, err
	}
	if err = clarificationRunStatus(ctx, tx, m.RunID, "waiting_for_user"); err != nil {
		return m, err
	}
	for _, kind := range []domain.RunEventType{domain.EventToolApprovalRequested, domain.EventTurnCompleted, domain.EventRunWaitingForUser} {
		if err = approvalEvent(ctx, tx, &m, kind); err != nil {
			return m, err
		}
	}
	return m, tx.Commit()
}

func (s *PostgresStore) ListToolApprovals(ctx context.Context, owner, workspace, run string) ([]domain.ToolApproval, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT a.record,r.status FROM tool_approvals a JOIN runs r ON r.id=a.run_id JOIN workspaces w ON w.id=r.workspace_id JOIN auth_memberships m ON m.workspace_id=w.id AND m.user_id=$3 WHERE r.id=$1 AND w.id=$2 AND w.owner_user_id=$3 AND w.deleted_at IS NULL ORDER BY (a.record->>'created_at')::timestamptz,a.id`, run, workspace, owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []domain.ToolApproval{}
	for rows.Next() {
		var data []byte
		var status string
		if err = rows.Scan(&data, &status); err != nil {
			return nil, err
		}
		var item domain.ToolApproval
		if err = json.Unmarshal(data, &item); err != nil {
			return nil, err
		}
		if item.Status == "pending" || item.Status == "approved" || status == "failed_recoverable" && (item.Status == "claimed" || item.Status == "consumed") {
			if status == "canceled" || status == "canceling" {
				item.Status = "canceled"
			} else if !item.ExpiresAt.After(time.Now()) {
				item.Status = "expired"
			}
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
