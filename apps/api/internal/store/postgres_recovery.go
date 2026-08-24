package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/domain"
)

func (s *PostgresStore) RepairInterruptedRun(request domain.InterruptedRunRepair) (domain.InterruptedRunRepairResult, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return domain.InterruptedRunRepairResult{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`SELECT pg_advisory_xact_lock(hashtext($1))`, request.RunID); err != nil {
		return domain.InterruptedRunRepairResult{}, err
	}
	run, err := scanRun(tx.QueryRow(`
		SELECT id, COALESCE(workspace_id, 'default_workspace'), agent_id, conversation_id, status, error,
			runtime_snapshot, completion_contract, verification_status, started_at, execution_started_at,
			active_runtime_ms, heartbeat_at, completed_at, created_at, updated_at
		FROM runs WHERE id = $1 FOR UPDATE`, request.RunID))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.InterruptedRunRepairResult{}, ErrNotFound("run")
	}
	if err != nil {
		return domain.InterruptedRunRepairResult{}, err
	}
	if run.Status != domain.RunRunning || (run.HeartbeatAt != nil && !run.HeartbeatAt.Before(request.StaleBefore)) {
		return domain.InterruptedRunRepairResult{Run: run}, nil
	}

	var cursor int64
	if err := tx.QueryRow(`SELECT COALESCE(MAX(sequence), 0) FROM run_events WHERE run_id = $1`, request.RunID).Scan(&cursor); err != nil {
		return domain.InterruptedRunRepairResult{}, err
	}
	if cursor != request.ExpectedEventCursor {
		return domain.InterruptedRunRepairResult{}, errors.New("run event cursor changed during recovery")
	}

	now := time.Now().UTC()
	appended := make([]domain.RunEvent, 0, len(request.TerminalEvents))
	for _, item := range request.TerminalEvents {
		cursor++
		item.RunID = request.RunID
		prepared, err := prepareRunEvent(item, cursor, now)
		if err != nil {
			return domain.InterruptedRunRepairResult{}, err
		}
		payload, err := json.Marshal(prepared.Payload)
		if err != nil {
			return domain.InterruptedRunRepairResult{}, err
		}
		if _, err := tx.Exec(`
			INSERT INTO run_events (
				id, run_id, conversation_id, stage_id, turn_id, parent_event_id,
				type, schema_version, sequence, payload, timestamp
			) VALUES ($1,$2,NULLIF($3,''),NULLIF($4,''),NULLIF($5,''),NULLIF($6,''),$7,$8,$9,$10,$11)`,
			prepared.ID, prepared.RunID, prepared.ConversationID, prepared.StageID,
			prepared.TurnID, prepared.ParentEventID, string(prepared.Type),
			prepared.SchemaVersion, prepared.Sequence, payload, prepared.Timestamp); err != nil {
			return domain.InterruptedRunRepairResult{}, err
		}
		appended = append(appended, prepared)
	}
	if _, err := tx.Exec(`
		UPDATE collaboration_steps
		SET status=$1, error=$2, updated_at=$3
		WHERE run_id=$4 AND status=$5`,
		string(domain.CollaborationStepFailed), strings.TrimSpace(request.ErrorMessage), now,
		request.RunID, string(domain.CollaborationStepRunning)); err != nil {
		return domain.InterruptedRunRepairResult{}, err
	}

	run, err = scanRun(tx.QueryRow(`
		UPDATE runs
		SET status = $1,
			error = $2,
			active_runtime_ms = active_runtime_ms + CASE
				WHEN execution_started_at IS NOT NULL
				THEN GREATEST(0, FLOOR(EXTRACT(EPOCH FROM ($3 - execution_started_at)) * 1000)::bigint)
				ELSE 0
			END,
			execution_started_at = NULL,
			completed_at = $3,
			updated_at = $3
		WHERE id = $4
		RETURNING id, workspace_id, agent_id, conversation_id, status, error,
			runtime_snapshot, completion_contract, verification_status, started_at, execution_started_at,
			active_runtime_ms, heartbeat_at, completed_at, created_at, updated_at`,
		string(domain.RunFailedRecoverable), strings.TrimSpace(request.ErrorMessage), now, request.RunID))
	if err != nil {
		return domain.InterruptedRunRepairResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.InterruptedRunRepairResult{}, err
	}
	return domain.InterruptedRunRepairResult{Run: run, AppendedEvents: appended, Applied: true}, nil
}
