package store

import (
	"database/sql"
	"encoding/json"
	"errors"

	"agentflow-platform/apps/api/internal/domain"
)

func (s *PostgresStore) CreateModelRequestRecord(record domain.ModelRequestRecord) (domain.ModelRequestRecord, error) {
	if err := validateModelRequestRecord(record); err != nil {
		return domain.ModelRequestRecord{}, err
	}
	parameters, err := json.Marshal(record.Envelope.Parameters)
	if err != nil {
		return domain.ModelRequestRecord{}, err
	}
	sourceTokenBreakdown, err := json.Marshal(record.Envelope.SourceTokenBreakdown)
	if err != nil {
		return domain.ModelRequestRecord{}, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return domain.ModelRequestRecord{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.Exec(`SELECT pg_advisory_xact_lock(hashtext($1))`, record.Envelope.RunID+":"+record.Envelope.ModelCallID); err != nil {
		return domain.ModelRequestRecord{}, err
	}
	err = tx.QueryRow(`
		INSERT INTO model_request_records (
			id, run_id, conversation_id, stage_id, turn_id, model_call_id, attempt,
			operation, provider, model, context_manifest_id, runtime_snapshot_hash,
			payload_hash, payload_bytes, parameters, source_token_breakdown, message_count, tool_count,
			capture_mode, capture_content, capture_content_hash, capture_original_bytes,
			capture_stored_bytes, capture_redacted, capture_redaction_strategy,
			capture_redaction_count, capture_truncated, capture_reconstructable,
			capture_expires_at, capture_expired, created_at
		) VALUES (
			$1,$2,$3,NULLIF($4,''),NULLIF($5,''),$6,
			(SELECT COALESCE(MAX(attempt),0)+1 FROM model_request_records WHERE run_id=$2 AND model_call_id=$6),
			$7,$8,$9,NULLIF($10,''),$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,$30
		) RETURNING attempt`,
		record.Envelope.ID, record.Envelope.RunID, record.Envelope.ConversationID,
		record.Envelope.StageID, record.Envelope.TurnID, record.Envelope.ModelCallID,
		record.Envelope.Operation, record.Envelope.Provider, record.Envelope.Model,
		record.Envelope.ContextManifestID, record.Envelope.RuntimeSnapshotHash,
		record.Envelope.PayloadHash, record.Envelope.PayloadBytes, parameters, sourceTokenBreakdown,
		record.Envelope.MessageCount, record.Envelope.ToolCount, string(record.Capture.Mode),
		record.Capture.Content, record.Capture.ContentHash, record.Capture.OriginalBytes,
		record.Capture.StoredBytes, record.Capture.Redacted, record.Capture.RedactionStrategy,
		record.Capture.RedactionCount, record.Capture.Truncated, record.Capture.Reconstructable,
		record.Capture.ExpiresAt, record.Capture.Expired, record.Envelope.CreatedAt).Scan(&record.Envelope.Attempt)
	if err != nil {
		return domain.ModelRequestRecord{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.ModelRequestRecord{}, err
	}
	return cloneModelRequestRecord(record), nil
}

func (s *PostgresStore) ListModelRequestRecords(runID string) ([]domain.ModelRequestRecord, error) {
	if _, err := s.db.Exec(`
		UPDATE model_request_records SET
			capture_content='', capture_content_hash='', capture_stored_bytes=0,
			capture_reconstructable=false, capture_expired=true
		WHERE run_id=$1 AND capture_content<>'' AND capture_expires_at IS NOT NULL AND capture_expires_at<=NOW()`, runID); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`
		SELECT id, run_id, conversation_id, COALESCE(stage_id,''), COALESCE(turn_id,''),
			model_call_id, attempt, operation, provider, model, COALESCE(context_manifest_id,''),
			runtime_snapshot_hash, payload_hash, payload_bytes, parameters, source_token_breakdown, message_count,
			tool_count, capture_mode, capture_content, capture_content_hash,
			capture_original_bytes, capture_stored_bytes, capture_redacted,
			capture_redaction_strategy, capture_redaction_count, capture_truncated,
			capture_reconstructable, capture_expires_at, capture_expired, created_at
		FROM model_request_records WHERE run_id=$1 ORDER BY created_at, id, attempt`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []domain.ModelRequestRecord{}
	for rows.Next() {
		item, err := scanModelRequestRecord(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(items) == 0 {
		if _, ok, err := s.GetRun(runID); err != nil {
			return nil, err
		} else if !ok {
			return nil, ErrNotFound("run")
		}
	}
	return items, nil
}

func scanModelRequestRecord(row scanner) (domain.ModelRequestRecord, error) {
	var record domain.ModelRequestRecord
	var parameters []byte
	var sourceTokenBreakdown []byte
	var mode string
	err := row.Scan(
		&record.Envelope.ID, &record.Envelope.RunID, &record.Envelope.ConversationID,
		&record.Envelope.StageID, &record.Envelope.TurnID, &record.Envelope.ModelCallID,
		&record.Envelope.Attempt, &record.Envelope.Operation, &record.Envelope.Provider,
		&record.Envelope.Model, &record.Envelope.ContextManifestID,
		&record.Envelope.RuntimeSnapshotHash, &record.Envelope.PayloadHash,
		&record.Envelope.PayloadBytes, &parameters, &sourceTokenBreakdown, &record.Envelope.MessageCount,
		&record.Envelope.ToolCount, &mode, &record.Capture.Content,
		&record.Capture.ContentHash, &record.Capture.OriginalBytes,
		&record.Capture.StoredBytes, &record.Capture.Redacted,
		&record.Capture.RedactionStrategy, &record.Capture.RedactionCount, &record.Capture.Truncated,
		&record.Capture.Reconstructable, &record.Capture.ExpiresAt, &record.Capture.Expired,
		&record.Envelope.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ModelRequestRecord{}, ErrNotFound("model request")
	}
	if err != nil {
		return domain.ModelRequestRecord{}, err
	}
	record.Capture.Mode = domain.ModelRequestCaptureMode(mode)
	if err := json.Unmarshal(parameters, &record.Envelope.Parameters); err != nil {
		return domain.ModelRequestRecord{}, err
	}
	if err := json.Unmarshal(sourceTokenBreakdown, &record.Envelope.SourceTokenBreakdown); err != nil {
		return domain.ModelRequestRecord{}, err
	}
	return record, nil
}
