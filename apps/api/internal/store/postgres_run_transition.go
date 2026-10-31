package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"agentflow-platform/apps/api/internal/domain"
)

func (s *PostgresStore) TransitionRun(request domain.RunTransition) (domain.RunTransitionResult, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return domain.RunTransitionResult{}, err
	}
	defer tx.Rollback()
	// Match event, clarification and repair lock order: event stream, then Run.
	if _, err := tx.Exec(`SELECT pg_advisory_xact_lock(hashtext($1))`, request.RunID); err != nil {
		return domain.RunTransitionResult{}, err
	}
	run, err := scanRun(tx.QueryRow(`SELECT id, workspace_id, agent_id, conversation_id, status, error,
		runtime_snapshot, completion_contract, verification_status, started_at, execution_started_at,
		active_runtime_ms, heartbeat_at, completed_at, created_at, updated_at FROM runs WHERE id=$1 FOR UPDATE`, request.RunID))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.RunTransitionResult{}, ErrNotFound("run")
	}
	if err != nil {
		return domain.RunTransitionResult{}, err
	}
	var cursor int64
	if err := tx.QueryRow(`SELECT COALESCE(MAX(sequence),0) FROM run_events WHERE run_id=$1`, run.ID).Scan(&cursor); err != nil {
		return domain.RunTransitionResult{}, err
	}
	result, err := PrepareRunTransition(run, request, cursor, time.Now().UTC())
	if err != nil || !result.Applied {
		return result, err
	}
	for _, event := range result.Events {
		payload, err := json.Marshal(event.Payload)
		if err != nil {
			return domain.RunTransitionResult{}, err
		}
		if _, err := tx.Exec(`INSERT INTO run_events (id,run_id,conversation_id,type,schema_version,sequence,payload,timestamp)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, event.ID, event.RunID, event.ConversationID, event.Type, event.SchemaVersion, event.Sequence, payload, event.Timestamp); err != nil {
			return domain.RunTransitionResult{}, err
		}
	}
	next := result.Run
	if _, err := tx.Exec(`UPDATE runs SET status=$2,error=$3,started_at=$4,execution_started_at=$5,
		active_runtime_ms=$6,heartbeat_at=$7,completed_at=$8,updated_at=$9 WHERE id=$1`,
		next.ID, next.Status, next.Error, next.StartedAt, next.ExecutionStartedAt, next.ActiveRuntimeMS, next.HeartbeatAt, next.CompletedAt, next.UpdatedAt); err != nil {
		return domain.RunTransitionResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.RunTransitionResult{}, err
	}
	return result, nil
}
