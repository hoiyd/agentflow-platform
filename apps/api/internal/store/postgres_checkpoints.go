package store

import (
	"bytes"
	"database/sql"
	"errors"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/domain"
)

func (s *PostgresStore) SaveStageCheckpoint(checkpoint domain.StageCheckpoint) (domain.StageCheckpoint, error) {
	if err := validateStageCheckpoint(checkpoint); err != nil {
		return domain.StageCheckpoint{}, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return domain.StageCheckpoint{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`SELECT pg_advisory_xact_lock(hashtext($1))`, checkpoint.RunID); err != nil {
		return domain.StageCheckpoint{}, err
	}
	existing, found, err := getStageCheckpoint(tx, checkpoint.RunID, checkpoint.StageID, true)
	if err != nil {
		return domain.StageCheckpoint{}, err
	}
	now := time.Now().UTC()
	if found {
		if err := validateCheckpointUpdate(existing, checkpoint); err != nil {
			return domain.StageCheckpoint{}, err
		}
		checkpoint.ID = existing.ID
		checkpoint.CreatedAt = existing.CreatedAt
	} else {
		if checkpoint.ID == "" {
			checkpoint.ID = newID("checkpoint")
		}
		checkpoint.CreatedAt = now
	}
	checkpoint.UpdatedAt = now
	stored, err := scanStageCheckpoint(tx.QueryRow(`
		INSERT INTO stage_checkpoints (
			id, provider, run_id, conversation_id, stage_id, status, input_hash,
			output_hash, runtime_snapshot_hash, tool_definitions_hash, event_cursor,
			error, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		ON CONFLICT (run_id, stage_id) DO UPDATE SET
			status=EXCLUDED.status, output_hash=EXCLUDED.output_hash,
			event_cursor=EXCLUDED.event_cursor, error=EXCLUDED.error,
			updated_at=EXCLUDED.updated_at
		RETURNING id, provider, run_id, conversation_id, stage_id, status, input_hash,
			output_hash, runtime_snapshot_hash, tool_definitions_hash, event_cursor,
			error, created_at, updated_at`,
		checkpoint.ID, checkpoint.Provider, checkpoint.RunID, checkpoint.ConversationID,
		checkpoint.StageID, string(checkpoint.Status), checkpoint.InputHash,
		checkpoint.OutputHash, checkpoint.RuntimeSnapshotHash, checkpoint.ToolDefinitionsHash,
		checkpoint.EventCursor, checkpoint.Error, checkpoint.CreatedAt, checkpoint.UpdatedAt))
	if err != nil {
		return domain.StageCheckpoint{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.StageCheckpoint{}, err
	}
	return stored, nil
}

func (s *PostgresStore) GetStageCheckpoint(runID string, stageID string) (domain.StageCheckpoint, bool, error) {
	return getStageCheckpoint(s.db, runID, stageID, false)
}

func (s *PostgresStore) ListStageCheckpoints(runID string) ([]domain.StageCheckpoint, error) {
	rows, err := s.db.Query(`
		SELECT id, provider, run_id, conversation_id, stage_id, status, input_hash,
			output_hash, runtime_snapshot_hash, tool_definitions_hash, event_cursor,
			error, created_at, updated_at
		FROM stage_checkpoints WHERE run_id=$1 ORDER BY event_cursor, stage_id`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]domain.StageCheckpoint, 0)
	for rows.Next() {
		item, err := scanStageCheckpoint(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *PostgresStore) BeginToolEffect(effect domain.ToolEffectRecord) (domain.ToolEffectRecord, bool, error) {
	if err := validateToolEffect(effect); err != nil {
		return domain.ToolEffectRecord{}, false, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return domain.ToolEffectRecord{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`SELECT pg_advisory_xact_lock(hashtext($1))`, effect.IdempotencyKey); err != nil {
		return domain.ToolEffectRecord{}, false, err
	}
	existing, found, err := getToolEffect(tx, effect.IdempotencyKey, true)
	if err != nil {
		return domain.ToolEffectRecord{}, false, err
	}
	if found {
		if existing.RequestHash != effect.RequestHash || existing.ToolName != effect.ToolName || existing.RunID != effect.RunID {
			return domain.ToolEffectRecord{}, false, errors.New("idempotency key was already used for a different tool request")
		}
		return existing, false, nil
	}
	now := time.Now().UTC()
	effect.Status = domain.ToolEffectExecuting
	effect.Version = 1
	effect.CreatedAt = now
	effect.UpdatedAt = now
	stored, err := scanToolEffect(tx.QueryRow(`
		INSERT INTO tool_effects (
			idempotency_key, version, run_id, stage_id, turn_id, tool_call_id, tool_name,
			definition_revision, request_hash, status, result, error, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,NULL,$11,$12,$13)
		RETURNING idempotency_key, version, run_id, stage_id, turn_id, tool_call_id,
			tool_name, definition_revision, request_hash, status, result, error, created_at, updated_at`,
		effect.IdempotencyKey, effect.Version, effect.RunID, effect.StageID, effect.TurnID,
		effect.ToolCallID, effect.ToolName, effect.DefinitionRevision, effect.RequestHash,
		string(effect.Status), effect.Error, effect.CreatedAt, effect.UpdatedAt))
	if err != nil {
		return domain.ToolEffectRecord{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return domain.ToolEffectRecord{}, false, err
	}
	return stored, true, nil
}

func (s *PostgresStore) CompleteToolEffect(idempotencyKey string, result []byte) (domain.ToolEffectRecord, error) {
	return s.updateToolEffect(idempotencyKey, func(existing domain.ToolEffectRecord) (domain.ToolEffectStatus, []byte, string, error) {
		if existing.Status == domain.ToolEffectCommitted {
			if !bytes.Equal(existing.Result, result) {
				return "", nil, "", errors.New("committed tool effect result differs")
			}
			return existing.Status, existing.Result, existing.Error, nil
		}
		if existing.Status != domain.ToolEffectExecuting {
			return "", nil, "", errors.New("tool effect is not executing")
		}
		return domain.ToolEffectCommitted, append([]byte(nil), result...), "", nil
	})
}

func (s *PostgresStore) MarkToolEffectNeedsReconciliation(idempotencyKey string, errorMessage string) (domain.ToolEffectRecord, error) {
	return s.updateToolEffect(idempotencyKey, func(existing domain.ToolEffectRecord) (domain.ToolEffectStatus, []byte, string, error) {
		if existing.Status == domain.ToolEffectCommitted || existing.Status == domain.ToolEffectCompensated {
			return "", nil, "", errors.New("terminal tool effect cannot require reconciliation")
		}
		return domain.ToolEffectNeedsReconciliation, existing.Result, strings.TrimSpace(errorMessage), nil
	})
}

func (s *PostgresStore) ListToolEffects(runID string) ([]domain.ToolEffectRecord, error) {
	rows, err := s.db.Query(`
		SELECT idempotency_key, version, run_id, stage_id, turn_id, tool_call_id, tool_name,
			definition_revision, request_hash, status, result, error, created_at, updated_at
		FROM tool_effects WHERE run_id=$1 ORDER BY created_at, idempotency_key`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]domain.ToolEffectRecord, 0)
	for rows.Next() {
		item, err := scanToolEffect(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *PostgresStore) CommitToolEffectReconciliation(mutation domain.ToolEffectReconciliation) (domain.ToolEffectRecord, domain.RunEvent, bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return domain.ToolEffectRecord{}, domain.RunEvent{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`SELECT pg_advisory_xact_lock(hashtext($1))`, mutation.Event.RunID); err != nil {
		return domain.ToolEffectRecord{}, domain.RunEvent{}, false, err
	}
	var duplicate bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM run_events WHERE id=$1)`, mutation.Event.ID).Scan(&duplicate); err != nil {
		return domain.ToolEffectRecord{}, domain.RunEvent{}, false, err
	}
	if duplicate {
		current, found, err := getToolEffect(tx, mutation.IdempotencyKey, false)
		if err != nil {
			return domain.ToolEffectRecord{}, domain.RunEvent{}, false, err
		}
		if !found {
			return domain.ToolEffectRecord{}, domain.RunEvent{}, false, ErrNotFound("tool effect")
		}
		if err := tx.Commit(); err != nil {
			return domain.ToolEffectRecord{}, domain.RunEvent{}, false, err
		}
		return current, domain.RunEvent{}, false, nil
	}
	current, found, err := getToolEffect(tx, mutation.IdempotencyKey, true)
	if err != nil {
		return domain.ToolEffectRecord{}, domain.RunEvent{}, false, err
	}
	if !found {
		return domain.ToolEffectRecord{}, domain.RunEvent{}, false, ErrNotFound("tool effect")
	}
	prepared, err := prepareToolEffectReconciliation(current, mutation)
	if err != nil {
		return domain.ToolEffectRecord{}, domain.RunEvent{}, false, err
	}
	now := time.Now().UTC()
	prepared.Event.Timestamp = now
	createdEvent, payload, err := preparePostgresRunEvent(prepared.Event, now)
	if err != nil {
		return domain.ToolEffectRecord{}, domain.RunEvent{}, false, err
	}
	stored, err := scanToolEffect(tx.QueryRow(`
		UPDATE tool_effects SET version=$1, status=$2, result=$3, error=$4, updated_at=$5
		WHERE idempotency_key=$6 AND version=$7
		RETURNING idempotency_key, version, run_id, stage_id, turn_id, tool_call_id,
			tool_name, definition_revision, request_hash, status, result, error, created_at, updated_at`,
		current.Version+1, string(prepared.NextStatus), prepared.Result, strings.TrimSpace(prepared.Error), now,
		current.IdempotencyKey, current.Version))
	if err != nil {
		return domain.ToolEffectRecord{}, domain.RunEvent{}, false, err
	}
	err = tx.QueryRow(`
		INSERT INTO run_events (id, run_id, conversation_id, stage_id, turn_id, parent_event_id, type, schema_version, sequence, payload, timestamp)
		VALUES ($1,$2,NULLIF($3,''),NULLIF($4,''),NULLIF($5,''),NULLIF($6,''),$7,$8,
			(SELECT COALESCE(MAX(sequence),0)+1 FROM run_events WHERE run_id=$2),$9,$10)
		RETURNING sequence`, createdEvent.ID, createdEvent.RunID, createdEvent.ConversationID,
		createdEvent.StageID, createdEvent.TurnID, createdEvent.ParentEventID, string(createdEvent.Type),
		createdEvent.SchemaVersion, payload, createdEvent.Timestamp).Scan(&createdEvent.Sequence)
	if err != nil {
		return domain.ToolEffectRecord{}, domain.RunEvent{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return domain.ToolEffectRecord{}, domain.RunEvent{}, false, err
	}
	return stored, createdEvent, true, nil
}

type checkpointQuery interface {
	QueryRow(string, ...any) *sql.Row
}

func getStageCheckpoint(query checkpointQuery, runID, stageID string, lock bool) (domain.StageCheckpoint, bool, error) {
	suffix := ""
	if lock {
		suffix = " FOR UPDATE"
	}
	item, err := scanStageCheckpoint(query.QueryRow(`
		SELECT id, provider, run_id, conversation_id, stage_id, status, input_hash,
			output_hash, runtime_snapshot_hash, tool_definitions_hash, event_cursor,
			error, created_at, updated_at
		FROM stage_checkpoints WHERE run_id=$1 AND stage_id=$2`+suffix, runID, stageID))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.StageCheckpoint{}, false, nil
	}
	return item, err == nil, err
}

func (s *PostgresStore) updateToolEffect(idempotencyKey string, mutate func(domain.ToolEffectRecord) (domain.ToolEffectStatus, []byte, string, error)) (domain.ToolEffectRecord, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return domain.ToolEffectRecord{}, err
	}
	defer func() { _ = tx.Rollback() }()
	existing, found, err := getToolEffect(tx, idempotencyKey, true)
	if err != nil {
		return domain.ToolEffectRecord{}, err
	}
	if !found {
		return domain.ToolEffectRecord{}, ErrNotFound("tool effect")
	}
	status, result, message, err := mutate(existing)
	if err != nil {
		return domain.ToolEffectRecord{}, err
	}
	stored, err := scanToolEffect(tx.QueryRow(`
		UPDATE tool_effects SET version=$1, status=$2, result=$3, error=$4, updated_at=$5
		WHERE idempotency_key=$6
		RETURNING idempotency_key, version, run_id, stage_id, turn_id, tool_call_id,
			tool_name, definition_revision, request_hash, status, result, error, created_at, updated_at`,
		existing.Version+1, string(status), result, message, time.Now().UTC(), idempotencyKey))
	if err != nil {
		return domain.ToolEffectRecord{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.ToolEffectRecord{}, err
	}
	return stored, nil
}

func getToolEffect(query checkpointQuery, idempotencyKey string, lock bool) (domain.ToolEffectRecord, bool, error) {
	suffix := ""
	if lock {
		suffix = " FOR UPDATE"
	}
	item, err := scanToolEffect(query.QueryRow(`
		SELECT idempotency_key, version, run_id, stage_id, turn_id, tool_call_id, tool_name,
			definition_revision, request_hash, status, result, error, created_at, updated_at
		FROM tool_effects WHERE idempotency_key=$1`+suffix, idempotencyKey))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ToolEffectRecord{}, false, nil
	}
	return item, err == nil, err
}

func scanStageCheckpoint(row scanner) (domain.StageCheckpoint, error) {
	var item domain.StageCheckpoint
	var status string
	err := row.Scan(&item.ID, &item.Provider, &item.RunID, &item.ConversationID,
		&item.StageID, &status, &item.InputHash, &item.OutputHash,
		&item.RuntimeSnapshotHash, &item.ToolDefinitionsHash, &item.EventCursor,
		&item.Error, &item.CreatedAt, &item.UpdatedAt)
	item.Status = domain.StageCheckpointStatus(status)
	return item, err
}

func scanToolEffect(row scanner) (domain.ToolEffectRecord, error) {
	var item domain.ToolEffectRecord
	var status string
	var result []byte
	err := row.Scan(&item.IdempotencyKey, &item.Version, &item.RunID, &item.StageID, &item.TurnID,
		&item.ToolCallID, &item.ToolName, &item.DefinitionRevision, &item.RequestHash, &status, &result,
		&item.Error, &item.CreatedAt, &item.UpdatedAt)
	item.Status = domain.ToolEffectStatus(status)
	item.Result = append([]byte(nil), result...)
	return item, err
}
