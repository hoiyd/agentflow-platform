package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/domain"
)

func (s *PostgresStore) CreateChildRun(request domain.ChildRunRequest) (domain.Run, domain.RunDelegation, error) {
	d := request.Delegation
	if err := validateChildRunRequest(request); err != nil {
		return domain.Run{}, domain.RunDelegation{}, err
	}
	parent, ok, err := s.GetRun(d.ParentRunID)
	if err != nil || !ok {
		if err == nil {
			err = errors.New("parent run not found")
		}
		return domain.Run{}, domain.RunDelegation{}, err
	}
	if _, ok, err := s.GetAgent(d.AgentID); err != nil || !ok {
		if err == nil {
			err = errors.New("agent not found")
		}
		return domain.Run{}, domain.RunDelegation{}, err
	}
	snapshotJSON, err := json.Marshal(request.RuntimeSnapshot)
	if err != nil {
		return domain.Run{}, domain.RunDelegation{}, err
	}
	now := time.Now().UTC()
	run := domain.Run{
		ID: newID("run"), WorkspaceID: parent.WorkspaceID, AgentID: d.AgentID,
		ConversationID: parent.ConversationID, Status: domain.RunQueued,
		RuntimeSnapshot:    cloneRuntimeSnapshot(request.RuntimeSnapshot),
		VerificationStatus: domain.VerificationNotRequired, CreatedAt: now, UpdatedAt: now,
	}
	d.WorkspaceID, d.ConversationID, d.ChildRunID = parent.WorkspaceID, parent.ConversationID, run.ID
	d.Status, d.Task, d.CreatedAt, d.UpdatedAt = domain.DelegationCreated, strings.TrimSpace(d.Task), now, now
	tx, err := s.db.Begin()
	if err != nil {
		return domain.Run{}, domain.RunDelegation{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.Exec(`
		INSERT INTO runs (id, workspace_id, agent_id, conversation_id, status, error, runtime_snapshot, completion_contract, verification_status, started_at, execution_started_at, active_runtime_ms, heartbeat_at, completed_at, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,'',$6,'null'::jsonb,$7,NULL,NULL,0,NULL,NULL,$8,$8)`,
		run.ID, run.WorkspaceID, run.AgentID, run.ConversationID, string(run.Status), snapshotJSON, string(run.VerificationStatus), now); err != nil {
		return domain.Run{}, domain.RunDelegation{}, err
	}
	if _, err = tx.Exec(`INSERT INTO run_delegations (
		id, workspace_id, conversation_id, parent_run_id, parent_turn_id, parent_stage_id,
		child_run_id, agent_id, depth, status, block_reason, task, timeout_ms, created_at, updated_at
	) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$14)`,
		d.ID, d.WorkspaceID, d.ConversationID, d.ParentRunID, d.ParentTurnID, d.ParentStageID,
		d.ChildRunID, d.AgentID, d.Depth, string(d.Status), string(d.BlockReason), d.Task, d.TimeoutMS, now); err != nil {
		return domain.Run{}, domain.RunDelegation{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.Run{}, domain.RunDelegation{}, err
	}
	return run, d, nil
}

func (s *PostgresStore) UpdateRunDelegation(id string, result domain.DelegationResult) (domain.RunDelegation, error) {
	if err := validateDelegationResult(result); err != nil {
		return domain.RunDelegation{}, err
	}
	return scanDelegation(s.db.QueryRow(`UPDATE run_delegations SET
		status=$1, block_reason=$2, summary=$3, output_ref=$4, output_hash=$5, output_bytes=$6,
		summary_truncated=$7, error=$8, updated_at=$9 WHERE id=$10
		RETURNING id, workspace_id, conversation_id, parent_run_id, parent_turn_id,
		parent_stage_id, child_run_id, agent_id, depth, status, block_reason, task, summary,
		output_ref, output_hash, output_bytes, summary_truncated, timeout_ms, error,
		created_at, updated_at`, string(result.Status), string(result.BlockReason), strings.TrimSpace(result.Summary),
		strings.TrimSpace(result.OutputRef), strings.TrimSpace(result.OutputHash), result.OutputBytes,
		result.SummaryTruncated, strings.TrimSpace(result.Error), time.Now().UTC(), strings.TrimSpace(id)))
}

func (s *PostgresStore) GetRunDelegation(id string) (domain.RunDelegation, bool, error) {
	item, err := scanDelegation(s.db.QueryRow(delegationSelect+` WHERE id=$1`, strings.TrimSpace(id)))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.RunDelegation{}, false, nil
	}
	return item, err == nil, err
}

func (s *PostgresStore) GetParentDelegation(childRunID string) (domain.RunDelegation, bool, error) {
	item, err := scanDelegation(s.db.QueryRow(delegationSelect+` WHERE child_run_id=$1`, strings.TrimSpace(childRunID)))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.RunDelegation{}, false, nil
	}
	return item, err == nil, err
}

func (s *PostgresStore) ListRunDelegations(parentRunID string) ([]domain.RunDelegation, error) {
	rows, err := s.db.Query(delegationSelect+` WHERE parent_run_id=$1 ORDER BY created_at, id`, strings.TrimSpace(parentRunID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []domain.RunDelegation{}
	for rows.Next() {
		item, err := scanDelegation(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *PostgresStore) ListActiveRunDelegations() ([]domain.RunDelegation, error) {
	rows, err := s.db.Query(delegationSelect+` WHERE status IN ($1,$2) ORDER BY created_at, id`, string(domain.DelegationCreated), string(domain.DelegationRunning))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []domain.RunDelegation{}
	for rows.Next() {
		item, err := scanDelegation(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

const delegationSelect = `SELECT id, workspace_id, conversation_id, parent_run_id,
	parent_turn_id, parent_stage_id, child_run_id, agent_id, depth, status, block_reason, task,
	summary, output_ref, output_hash, output_bytes, summary_truncated, timeout_ms,
	error, created_at, updated_at FROM run_delegations`

type delegationScanner interface{ Scan(...any) error }

func scanDelegation(row delegationScanner) (domain.RunDelegation, error) {
	var item domain.RunDelegation
	var status, blockReason string
	err := row.Scan(&item.ID, &item.WorkspaceID, &item.ConversationID, &item.ParentRunID,
		&item.ParentTurnID, &item.ParentStageID, &item.ChildRunID, &item.AgentID, &item.Depth,
		&status, &blockReason, &item.Task, &item.Summary, &item.OutputRef, &item.OutputHash, &item.OutputBytes,
		&item.SummaryTruncated, &item.TimeoutMS, &item.Error, &item.CreatedAt, &item.UpdatedAt)
	item.Status = domain.DelegationStatus(status)
	item.BlockReason = domain.DelegationBlockReason(blockReason)
	return item, err
}
