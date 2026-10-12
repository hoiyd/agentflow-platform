package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/clarification"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/redaction"
)

const clarificationSchema = `CREATE TABLE IF NOT EXISTS run_clarifications (
 id text PRIMARY KEY, run_id text NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
 conversation_id text NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
 stage_id text NOT NULL DEFAULT '', turn_id text NOT NULL, tool_call_id text NOT NULL DEFAULT '',
 version bigint NOT NULL DEFAULT 1, status text NOT NULL CHECK(status IN('pending','answered','applied','withdrawn')),
 questions jsonb NOT NULL, answers jsonb NOT NULL DEFAULT '{}'::jsonb,
 answer_key text NOT NULL DEFAULT '', checkpoint jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT NOW(), expires_at timestamptz NOT NULL DEFAULT NOW()+INTERVAL '24 hours',
 answered_at timestamptz, applied_at timestamptz
)`
const clarificationColumns = `id,run_id,conversation_id,stage_id,turn_id,tool_call_id,version,status,questions,answers,created_at,expires_at,answered_at,applied_at`

func scanClarification(row scanner) (domain.Clarification, error) {
	var c domain.Clarification
	var q, a []byte
	err := row.Scan(&c.ID, &c.RunID, &c.ConversationID, &c.StageID, &c.TurnID, &c.ToolCallID, &c.Version, &c.Status, &q, &a, &c.CreatedAt, &c.ExpiresAt, &c.AnsweredAt, &c.AppliedAt)
	if err != nil {
		return c, err
	}
	if err = json.Unmarshal(q, &c.Questions); err != nil {
		return c, err
	}
	err = json.Unmarshal(a, &c.Answers)
	return c, err
}

func (s *PostgresStore) SuspendClarification(ctx context.Context, input domain.Clarification, checkpoint json.RawMessage) (clarification.Mutation, error) {
	c := clarification.Mutation{Clarification: input}
	if c.RunID == "" || c.ConversationID == "" || c.TurnID == "" || len(checkpoint) > clarification.MaxCheckpointBytes || !json.Valid(checkpoint) {
		return c, clarification.ErrInvalid
	}
	if err := clarification.ValidateQuestions(c.Questions); err != nil {
		return c, err
	}
	if err := redaction.ValidateValue(checkpoint); err != nil {
		return c, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return c, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, c.RunID); err != nil {
		return c, err
	}
	var owner, workspace string
	if err = tx.QueryRowContext(ctx, `SELECT w.owner_user_id,r.workspace_id FROM runs r JOIN workspaces w ON w.id=r.workspace_id WHERE r.id=$1 AND r.conversation_id=$2`, c.RunID, c.ConversationID).Scan(&owner, &workspace); err != nil {
		return c, err
	}
	if err = lockInbox(ctx, tx, owner, workspace, c.ConversationID); err != nil {
		return c, err
	}
	var status string
	if err = tx.QueryRowContext(ctx, `SELECT status FROM runs WHERE id=$1 FOR UPDATE`, c.RunID).Scan(&status); err != nil {
		return c, err
	}
	if status != "running" {
		return c, clarification.ErrConflict
	}
	var realTurn bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM run_events WHERE run_id=$1 AND turn_id=$2 AND type='turn.started')`, c.RunID, c.TurnID).Scan(&realTurn); err != nil {
		return c, err
	}
	if !realTurn {
		return c, clarification.ErrInvalid
	}
	if c.ToolCallID != "" {
		var realCall bool
		if err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM run_events WHERE run_id=$1 AND turn_id=$2 AND type='tool.completed' AND payload->>'tool_call_id'=$3 AND payload->>'tool_name'='request_user_input' AND COALESCE(payload->>'error','')='')", c.RunID, c.TurnID, c.ToolCallID).Scan(&realCall); err != nil {
			return c, err
		}
		if !realCall {
			return c, clarification.ErrInvalid
		}
	}
	if c.StageID != "" {
		var realStage bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM collaboration_steps WHERE id=$1 AND run_id=$2)`, c.StageID, c.RunID).Scan(&realStage); err != nil {
			return c, err
		}
		if !realStage {
			return c, clarification.ErrInvalid
		}
	}
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM run_clarifications WHERE run_id=$1`, c.RunID).Scan(&count); err != nil {
		return c, err
	}
	if count >= 16 {
		return c, clarification.ErrInvalid
	}
	q, _ := json.Marshal(c.Questions)
	c, err = scanClarificationMutation(tx.QueryRowContext(ctx, `INSERT INTO run_clarifications(id,run_id,conversation_id,stage_id,turn_id,tool_call_id,status,questions,checkpoint) VALUES($1,$2,$3,$4,$5,$6,'pending',$7,$8) RETURNING `+clarificationColumns, NewID("question"), c.RunID, c.ConversationID, c.StageID, c.TurnID, c.ToolCallID, q, checkpoint))
	if err != nil {
		return c, err
	}
	if err = clarificationRunStatus(ctx, tx, c.RunID, "waiting_for_user"); err != nil {
		return c, err
	}
	// Close the suspended Turn in the same commit as waiting. A crash after
	// this transaction cannot leave an orphan Turn on a non-running Run.
	var closed bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM run_events WHERE run_id=$1 AND turn_id=$2 AND type IN('turn.completed','turn.failed','turn.canceled'))`, c.RunID, c.TurnID).Scan(&closed); err != nil {
		return c, err
	}
	if !closed {
		if err = clarificationEvent(ctx, tx, &c, domain.EventTurnCompleted); err != nil {
			return c, err
		}
	}
	if err = clarificationEvent(ctx, tx, &c, domain.EventRunWaitingForUser); err != nil {
		return c, err
	}
	return c, tx.Commit()
}

func (s *PostgresStore) ListClarifications(ctx context.Context, owner, workspace, run string) ([]domain.Clarification, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+clarificationColumns+` FROM run_clarifications WHERE run_id=$1 AND EXISTS(SELECT 1 FROM runs r JOIN workspaces w ON w.id=r.workspace_id JOIN auth_memberships m ON m.workspace_id=w.id AND m.user_id=$3 WHERE r.id=$1 AND w.id=$2 AND w.owner_user_id=$3 AND w.deleted_at IS NULL) ORDER BY created_at,id`, run, workspace, owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []domain.Clarification{}
	current, _, err := s.GetRun(run)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		c, err := scanClarification(rows)
		if err != nil {
			return nil, err
		}
		if c.Status == "pending" || c.Status == "answered" {
			if current.Status == domain.RunCanceled || current.Status == domain.RunCanceling {
				c.Status = "canceled"
			} else if !c.ExpiresAt.After(time.Now()) && c.Status == "pending" {
				c.Status = "expired"
			}
		}
		items = append(items, c)
	}
	return items, rows.Err()
}

func lockClarification(ctx context.Context, tx *sql.Tx, owner, workspace, run, id string) (domain.Clarification, string, error) {
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, run); err != nil {
		return domain.Clarification{}, "", err
	}
	var conv string
	if err := tx.QueryRowContext(ctx, `SELECT conversation_id FROM runs WHERE id=$1 AND workspace_id=$2`, run, workspace).Scan(&conv); err != nil {
		return domain.Clarification{}, "", ErrNotFound("run")
	}
	if err := lockInbox(ctx, tx, owner, workspace, conv); err != nil {
		return domain.Clarification{}, "", err
	}
	var status string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM runs WHERE id=$1 FOR UPDATE`, run).Scan(&status); err != nil {
		return domain.Clarification{}, "", err
	}
	c, err := scanClarification(tx.QueryRowContext(ctx, `SELECT `+clarificationColumns+` FROM run_clarifications WHERE id=$1 AND run_id=$2 FOR UPDATE`, id, run))
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound("clarification")
	}
	return c, status, err
}

func (s *PostgresStore) AnswerClarification(ctx context.Context, owner, workspace, run, id string, input domain.ClarificationAnswer) (clarification.Mutation, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return clarification.Mutation{}, err
	}
	defer tx.Rollback()
	record, status, err := lockClarification(ctx, tx, owner, workspace, run, id)
	c := clarification.Mutation{Clarification: record}
	if err != nil {
		return c, err
	}
	if len(input.IdempotencyKey) == 0 || len(input.IdempotencyKey) > 128 || input.Version != c.Version {
		return c, clarification.ErrInvalid
	}
	if err = clarification.ValidateAnswers(c.Questions, input.Answers); err != nil {
		return c, err
	}
	answers, _ := json.Marshal(input.Answers)
	if c.Status == "answered" || c.Status == "applied" {
		var same bool
		err = tx.QueryRowContext(ctx, `SELECT answer_key=$2 AND answers=$3::jsonb FROM run_clarifications WHERE id=$1`, id, input.IdempotencyKey, answers).Scan(&same)
		if err != nil {
			return c, err
		}
		if !same {
			return c, clarification.ErrConflict
		}
		return c, nil
	}
	if c.Status != "pending" || status != "waiting_for_user" || !c.ExpiresAt.After(time.Now()) {
		return c, clarification.ErrConflict
	}
	c, err = scanClarificationMutation(tx.QueryRowContext(ctx, `UPDATE run_clarifications SET answers=$2,answer_key=$3,status='answered',answered_at=NOW() WHERE id=$1 RETURNING `+clarificationColumns, id, answers, input.IdempotencyKey))
	if err != nil {
		return c, err
	}
	parts := []string{}
	for _, q := range c.Questions {
		parts = append(parts, q.Question+"\n"+input.Answers[q.ID])
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO messages(id,workspace_id,conversation_id,role,content,created_at) VALUES($1,$2,$3,'user',$4,NOW())`, c.ID, workspace, c.ConversationID, strings.Join(parts, "\n\n")); err != nil {
		return c, err
	}
	if err = clarificationEvent(ctx, tx, &c, domain.EventClarificationAnswered); err != nil {
		return c, err
	}
	return c, tx.Commit()
}

func (s *PostgresStore) ClaimClarification(ctx context.Context, owner, workspace, run, id string) (clarification.Mutation, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return clarification.Mutation{}, err
	}
	defer tx.Rollback()
	record, status, err := lockClarification(ctx, tx, owner, workspace, run, id)
	c := clarification.Mutation{Clarification: record}
	if err != nil {
		return c, err
	}
	if !(c.Status == "answered" && status == "waiting_for_user" || c.Status == "applied" && status == "failed_recoverable") {
		return c, clarification.ErrConflict
	}
	var latest string
	if err = tx.QueryRowContext(ctx, `SELECT id FROM run_clarifications WHERE run_id=$1 ORDER BY created_at DESC,id DESC LIMIT 1`, run).Scan(&latest); err != nil {
		return c, err
	}
	if latest != c.ID {
		return c, clarification.ErrConflict
	}
	if status == "failed_recoverable" {
		var uncertain bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM tool_effects WHERE run_id=$1 AND (status IN('prepared','executing','needs_reconciliation','reconciling') OR created_at>$2 AND status='committed'))`, run, c.CreatedAt).Scan(&uncertain); err != nil {
			return c, err
		}
		if uncertain {
			return c, clarification.ErrConflict
		}
	}
	var checkpoint json.RawMessage
	if err = tx.QueryRowContext(ctx, `SELECT checkpoint FROM run_clarifications WHERE id=$1`, id).Scan(&checkpoint); err != nil {
		return c, err
	}
	c, err = scanClarificationMutation(tx.QueryRowContext(ctx, `UPDATE run_clarifications SET status='applied',applied_at=NOW() WHERE id=$1 RETURNING `+clarificationColumns, id))
	if err != nil {
		return c, err
	}
	if err = clarificationRunStatus(ctx, tx, run, "running"); err != nil {
		return c, err
	}
	if err = clarificationEvent(ctx, tx, &c, domain.EventRunResumed); err != nil {
		return c, err
	}
	if status == "failed_recoverable" && c.StageID != "" {
		// Only this bound continuation can reopen its exact interrupted Stage.
		// Generic checkpoint transitions remain strict and stage recreation is not used.
		var stageStatus string
		if err = tx.QueryRowContext(ctx, `SELECT status FROM collaboration_steps WHERE id=$1 AND run_id=$2 FOR UPDATE`, c.StageID, run).Scan(&stageStatus); err != nil {
			return c, err
		}
		if stageStatus == "failed" {
			if _, err = tx.ExecContext(ctx, `UPDATE collaboration_steps SET status='running',error='',updated_at=NOW() WHERE id=$1`, c.StageID); err != nil {
				return c, err
			}
			if err = clarificationEvent(ctx, tx, &c, domain.EventStageStarted); err != nil {
				return c, err
			}
			result, updateErr := tx.ExecContext(ctx, `UPDATE stage_checkpoints SET status='executing',error='',event_cursor=(SELECT MAX(sequence) FROM run_events WHERE run_id=$2),updated_at=NOW() WHERE stage_id=$1 AND run_id=$2 AND status IN('executing','needs_reconciliation') AND runtime_snapshot_hash=(SELECT checkpoint->>'SnapshotHash' FROM run_clarifications WHERE id=$3)`, c.StageID, run, c.ID)
			if updateErr != nil {
				return c, updateErr
			}
			count, countErr := result.RowsAffected()
			if countErr != nil {
				return c, countErr
			}
			if count != 1 {
				return c, clarification.ErrConflict
			}
		}
	}
	c.Checkpoint = checkpoint
	return c, tx.Commit()
}

func (s *PostgresStore) WithdrawClarification(ctx context.Context, owner, workspace, run, id string, version int64) (clarification.Mutation, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return clarification.Mutation{}, err
	}
	defer tx.Rollback()
	record, status, err := lockClarification(ctx, tx, owner, workspace, run, id)
	c := clarification.Mutation{Clarification: record}
	if err != nil {
		return c, err
	}
	if version == c.Version && c.Status == "withdrawn" && status == "canceled" {
		return c, nil
	}
	if version != c.Version || c.Status != "pending" || status != "waiting_for_user" {
		return c, clarification.ErrConflict
	}
	c, err = scanClarificationMutation(tx.QueryRowContext(ctx, `UPDATE run_clarifications SET status='withdrawn' WHERE id=$1 RETURNING `+clarificationColumns, id))
	if err != nil {
		return c, err
	}
	if err = clarificationRunStatus(ctx, tx, run, "canceled"); err != nil {
		return c, err
	}
	if err = clarificationEvent(ctx, tx, &c, domain.EventRunCanceled); err != nil {
		return c, err
	}
	return c, tx.Commit()
}

func clarificationRunStatus(ctx context.Context, tx *sql.Tx, run, status string) error {
	_, err := tx.ExecContext(ctx, `UPDATE runs SET active_runtime_ms=active_runtime_ms+CASE WHEN status='running' AND execution_started_at IS NOT NULL THEN GREATEST(0,FLOOR(EXTRACT(EPOCH FROM(NOW()-execution_started_at))*1000)::bigint) ELSE 0 END,
 execution_started_at=CASE WHEN $2='running' THEN NOW() ELSE NULL END,heartbeat_at=NOW(),status=$2,error='',updated_at=NOW(),completed_at=CASE WHEN $2='canceled' THEN NOW() ELSE NULL END WHERE id=$1`, run, status)
	return err
}

func clarificationEvent(ctx context.Context, tx *sql.Tx, c *clarification.Mutation, kind domain.RunEventType) error {
	data, _ := json.Marshal(map[string]any{"status": map[domain.RunEventType]string{domain.EventRunWaitingForUser: "waiting_for_user", domain.EventRunResumed: "running", domain.EventRunCanceled: "canceled"}[kind], "kind": "clarification", "clarification": c.Clarification})
	if kind == domain.EventTurnCompleted {
		data, _ = json.Marshal(map[string]any{"stop_reason": "waiting_for_user", "clarification_id": c.ID})
	}
	if kind == domain.EventStageStarted {
		var role, agent, input string
		var iteration int
		if err := tx.QueryRowContext(ctx, `SELECT role,agent_id,input,iteration FROM collaboration_steps WHERE id=$1 AND run_id=$2`, c.StageID, c.RunID).Scan(&role, &agent, &input, &iteration); err != nil {
			return err
		}
		data, _ = json.Marshal(map[string]any{"name": role, "agent_id": agent, "input": input, "iteration": iteration, "clarification_id": c.ID})
	}
	// Use the existing durable sequence lock; checkpoint bytes are never events.
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, c.RunID); err != nil {
		return err
	}
	item := domain.RunEvent{ID: NewID("event"), RunID: c.RunID, ConversationID: c.ConversationID, StageID: c.StageID, TurnID: c.TurnID, Type: kind, SchemaVersion: 1, Timestamp: time.Now().UTC()}
	if err := json.Unmarshal(data, &item.Payload); err != nil {
		return err
	}
	err := tx.QueryRowContext(ctx, `INSERT INTO run_events(id,run_id,conversation_id,stage_id,turn_id,type,schema_version,sequence,payload,timestamp) VALUES($1,$2,$3,NULLIF($4,''),$5,$6,1,(SELECT COALESCE(MAX(sequence),0)+1 FROM run_events WHERE run_id=$2),$7,$8) RETURNING sequence`, item.ID, c.RunID, c.ConversationID, c.StageID, c.TurnID, kind, data, item.Timestamp).Scan(&item.Sequence)
	if err == nil {
		c.Events = append(c.Events, item)
	}
	return err
}

func scanClarificationMutation(row scanner) (clarification.Mutation, error) {
	record, err := scanClarification(row)
	return clarification.Mutation{Clarification: record}, err
}
