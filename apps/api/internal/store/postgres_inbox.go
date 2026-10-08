package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/failure"
	"agentflow-platform/apps/api/internal/redaction"
)

var (
	ErrInputConflict = failure.New(failure.Definition{Message: "input conflicts with its receipt or current execution state", Info: failure.Info{Code: "input_conflict", Source: "run_inbox", Category: failure.CategoryValidation}})
	ErrInputCapacity = failure.New(failure.Definition{Message: "conversation inbox capacity exceeded", Info: failure.Info{Code: "input_queue_full", Source: "run_inbox", Category: failure.CategoryCapacity, Retryable: true}})
	ErrInputInvalid  = failure.New(failure.Definition{Message: "invalid inbox input: kind, run, content and idempotency key are required", Info: failure.Info{Code: "input_invalid", Source: "run_inbox", Category: failure.CategoryValidation}})
)

const inboxSchema = `CREATE TABLE IF NOT EXISTS conversation_inputs (
 id text PRIMARY KEY,
 owner_user_id text NOT NULL REFERENCES auth_users(id),
 workspace_id bigint NOT NULL REFERENCES workspaces(id),
 conversation_id text NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
 run_id text NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
 request jsonb NOT NULL,
 status text NOT NULL DEFAULT 'queued' CHECK(status IN('queued','applied','withdrawn','expired')),
 applied_run_id text REFERENCES runs(id) ON DELETE CASCADE,
 stage_id text NOT NULL DEFAULT '', turn_id text NOT NULL DEFAULT '',
 created_at timestamptz NOT NULL DEFAULT NOW(),
 expires_at timestamptz NOT NULL DEFAULT NOW()+INTERVAL '24 hours',
 applied_at timestamptz
)`

const inputColumns = `id,conversation_id,run_id,request,CASE WHEN status='queued' AND expires_at<=NOW() THEN 'expired' ELSE status END,COALESCE(applied_run_id,''),stage_id,turn_id,created_at,expires_at,applied_at`

// Conversation row locking serializes queue bounds, withdrawal and consumption.
// The Workspace SHARE lock serializes lifecycle changes against every mutation.
func lockInbox(ctx context.Context, tx *sql.Tx, owner, workspace, conversation string) error {
	return lockInboxScope(ctx, tx, owner, workspace, conversation, true)
}

func lockInboxScope(ctx context.Context, tx *sql.Tx, owner, workspace, conversation string, writable bool) error {
	var id string
	err := tx.QueryRowContext(ctx, `SELECT c.id FROM conversations c JOIN workspaces w ON w.id=c.workspace_id
 WHERE c.id=$1 AND w.id=$2 AND w.owner_user_id=$3 AND (NOT $4::boolean OR w.status='active') AND w.deleted_at IS NULL
 AND EXISTS(SELECT 1 FROM auth_memberships WHERE user_id=$3 AND workspace_id=w.id)
 FOR UPDATE OF c FOR SHARE OF w`, conversation, workspace, owner, writable).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound("active conversation")
	}
	return err
}

func scanInput(row scanner) (domain.RunInput, error) {
	var item domain.RunInput
	var request []byte
	err := row.Scan(&item.ID, &item.ConversationID, &item.RunID, &request, &item.Status, &item.AppliedRunID, &item.StageID, &item.TurnID, &item.CreatedAt, &item.ExpiresAt, &item.AppliedAt)
	if err != nil {
		return item, err
	}
	var input domain.RunInputRequest
	if err = json.Unmarshal(request, &input); err != nil {
		return item, err
	}
	item.Kind = input.Kind
	item.Content = input.Content
	item.Mode = input.Mode
	item.AgentID = input.AgentID
	return item, nil
}

func (s *PostgresStore) EnqueueRunInput(ctx context.Context, owner, workspace, conversation string, input domain.RunInputRequest) (domain.RunInput, error) {
	input.Content = strings.TrimSpace(input.Content)
	if input.Kind != "steer" && input.Kind != "follow_up" || input.Content == "" || len(input.Content) > 8192 || input.RunID == "" || len(input.IdempotencyKey) == 0 || len(input.IdempotencyKey) > 128 {
		return domain.RunInput{}, ErrInputInvalid
	}
	if input.Mode == "" {
		input.Mode = "single"
	}
	if input.Mode != "single" && input.Mode != "multi_agent" && input.Mode != "autonomous" {
		return domain.RunInput{}, ErrInputInvalid
	}
	if err := redaction.ValidateValue(input); err != nil {
		return domain.RunInput{}, err
	}
	data, err := json.Marshal(input)
	if err != nil {
		return domain.RunInput{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.RunInput{}, err
	}
	defer tx.Rollback()
	if err = lockInbox(ctx, tx, owner, workspace, conversation); err != nil {
		return domain.RunInput{}, err
	}
	// Retain receipts for seven days. Applied inputs of active/recoverable Runs
	// cannot be purged because recovery still needs their original user content.
	if _, err = tx.ExecContext(ctx, `DELETE FROM conversation_inputs i USING runs r WHERE i.conversation_id=$1 AND i.run_id=r.id
 AND i.created_at<NOW()-INTERVAL '7 days' AND r.status IN('completed','failed','canceled')
 AND NOT EXISTS(SELECT 1 FROM runs applied WHERE applied.id=i.applied_run_id AND applied.status NOT IN('completed','failed','canceled'))`, conversation); err != nil {
		return domain.RunInput{}, err
	}
	var same bool
	err = tx.QueryRowContext(ctx, `SELECT request=$4::jsonb FROM conversation_inputs WHERE owner_user_id=$1 AND conversation_id=$2 AND request->>'idempotency_key'=$3`, owner, conversation, input.IdempotencyKey, data).Scan(&same)
	if err == nil {
		if !same {
			return domain.RunInput{}, ErrInputConflict
		}
		item, e := scanInput(tx.QueryRowContext(ctx, `SELECT `+inputColumns+` FROM conversation_inputs WHERE owner_user_id=$1 AND conversation_id=$2 AND request->>'idempotency_key'=$3`, owner, conversation, input.IdempotencyKey))
		return item, e
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return domain.RunInput{}, err
	}
	var status string
	if err = tx.QueryRowContext(ctx, `SELECT status FROM runs WHERE id=$1 AND conversation_id=$2 FOR UPDATE`, input.RunID, conversation).Scan(&status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.RunInput{}, ErrInputConflict
		}
		return domain.RunInput{}, err
	}
	if input.Kind == "steer" && status != "running" {
		return domain.RunInput{}, ErrInputConflict
	}
	var count, pending, bytes, runBytes int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*),COUNT(*) FILTER(WHERE status='queued' AND expires_at>NOW()),
 COALESCE(SUM(octet_length(request->>'content')) FILTER(WHERE status='queued' AND expires_at>NOW()),0),
 COALESCE(SUM(octet_length(request->>'content')) FILTER(WHERE run_id=$2 AND request->>'kind'='steer' AND status IN('queued','applied')),0)
 FROM conversation_inputs WHERE conversation_id=$1`, conversation, input.RunID).Scan(&count, &pending, &bytes, &runBytes); err != nil {
		return domain.RunInput{}, err
	}
	if count >= 256 || pending >= 16 || bytes+len(input.Content) > 65536 || input.Kind == "steer" && runBytes+len(input.Content) > 65536 {
		return domain.RunInput{}, ErrInputCapacity
	}
	item, err := scanInput(tx.QueryRowContext(ctx, `INSERT INTO conversation_inputs(id,owner_user_id,workspace_id,conversation_id,run_id,request)
 VALUES($1,$2,$3,$4,$5,$6) RETURNING `+inputColumns, NewID("input"), owner, workspace, conversation, input.RunID, data))
	if err != nil {
		return item, err
	}
	return item, tx.Commit()
}

func listInputs(ctx context.Context, tx *sql.Tx, conversation string) ([]domain.RunInput, error) {
	rows, err := tx.QueryContext(ctx, `SELECT `+inputColumns+` FROM conversation_inputs WHERE conversation_id=$1 ORDER BY created_at,id`, conversation)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []domain.RunInput{}
	for rows.Next() {
		item, err := scanInput(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *PostgresStore) ListRunInputs(ctx context.Context, owner, workspace, conversation string) ([]domain.RunInput, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err = lockInboxScope(ctx, tx, owner, workspace, conversation, false); err != nil {
		return nil, err
	}
	items, err := listInputs(ctx, tx, conversation)
	if err != nil {
		return nil, err
	}
	return items, tx.Commit()
}

func (s *PostgresStore) WithdrawRunInput(ctx context.Context, owner, workspace, conversation, id string) (domain.RunInput, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.RunInput{}, err
	}
	defer tx.Rollback()
	if err = lockInbox(ctx, tx, owner, workspace, conversation); err != nil {
		return domain.RunInput{}, err
	}
	item, err := scanInput(tx.QueryRowContext(ctx, `UPDATE conversation_inputs SET status='withdrawn' WHERE id=$1 AND conversation_id=$2 AND (status='withdrawn' OR status='queued' AND expires_at>NOW()) RETURNING `+inputColumns, id, conversation))
	if errors.Is(err, sql.ErrNoRows) {
		return item, ErrInputConflict
	}
	if err != nil {
		return item, err
	}
	return item, tx.Commit()
}

// ConsumeSteering commits the user message and application location together.
// Returning previous applications lets a resumed Turn reconstruct the same inputs.
func (s *PostgresStore) ConsumeSteering(ctx context.Context, runID, stageID, turnID string) ([]domain.RunInput, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var conversation, workspace, owner string
	if err = tx.QueryRowContext(ctx, `SELECT r.conversation_id,r.workspace_id,w.owner_user_id FROM runs r JOIN workspaces w ON w.id=r.workspace_id WHERE r.id=$1`, runID).Scan(&conversation, &workspace, &owner); err != nil {
		return nil, err
	}
	if err = lockInbox(ctx, tx, owner, workspace, conversation); err != nil {
		return nil, err
	}
	var status string
	if err = tx.QueryRowContext(ctx, `SELECT status FROM runs WHERE id=$1 FOR UPDATE`, runID).Scan(&status); err != nil {
		return nil, err
	}
	if status != "running" {
		return nil, ErrInputConflict
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO messages(id,workspace_id,conversation_id,role,content,created_at)
 SELECT id,workspace_id,conversation_id,'user',request->>'content',NOW() FROM conversation_inputs
 WHERE run_id=$1 AND request->>'kind'='steer' AND status='queued' AND expires_at>NOW()`, runID); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE conversation_inputs SET status='applied',applied_run_id=$1,stage_id=$2,turn_id=$3,applied_at=NOW()
 WHERE run_id=$1 AND request->>'kind'='steer' AND status='queued' AND expires_at>NOW()`, runID, stageID, turnID); err != nil {
		return nil, err
	}
	all, err := listInputs(ctx, tx, conversation)
	if err != nil {
		return nil, err
	}
	items := []domain.RunInput{}
	for _, item := range all {
		if item.Kind == "steer" && item.AppliedRunID == runID && item.Status == "applied" {
			items = append(items, item)
		}
	}
	return items, tx.Commit()
}
