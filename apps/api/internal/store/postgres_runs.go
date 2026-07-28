package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/domain"
)

func (s *PostgresStore) CreateRun(agentID string, conversationID string, snapshot domain.RuntimeSnapshot) (domain.Run, error) {
	return s.CreateRunWithContract(agentID, conversationID, snapshot, nil)
}

func (s *PostgresStore) CreateRunWithContract(agentID string, conversationID string, snapshot domain.RuntimeSnapshot, contract *domain.CompletionContract) (domain.Run, error) {
	if _, ok, err := s.GetAgent(agentID); err != nil {
		return domain.Run{}, err
	} else if !ok {
		return domain.Run{}, errors.New("agent not found")
	}
	if _, ok, err := s.GetConversation(conversationID); err != nil {
		return domain.Run{}, err
	} else if !ok {
		return domain.Run{}, errors.New("conversation not found")
	}
	if snapshot.SchemaVersion != domain.CurrentRuntimeSnapshotVersion || snapshot.RunBudget == nil {
		return domain.Run{}, errors.New("runtime snapshot is required")
	}
	snapshotJSON, err := json.Marshal(snapshot)
	if err != nil {
		return domain.Run{}, err
	}
	contractJSON, err := json.Marshal(contract)
	if err != nil {
		return domain.Run{}, err
	}

	now := time.Now().UTC()
	run := domain.Run{
		ID:                 newID("run"),
		AgentID:            agentID,
		ConversationID:     conversationID,
		Status:             domain.RunQueued,
		RuntimeSnapshot:    cloneRuntimeSnapshot(snapshot),
		CompletionContract: cloneCompletionContract(contract),
		VerificationStatus: domain.VerificationNotRequired,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	if contract != nil {
		run.VerificationStatus = domain.VerificationPending
	}
	_, err = s.db.Exec(`
		INSERT INTO runs (id, agent_id, conversation_id, status, error, runtime_snapshot, completion_contract, verification_status, started_at, execution_started_at, active_runtime_ms, heartbeat_at, completed_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)`,
		run.ID, run.AgentID, run.ConversationID, string(run.Status), run.Error, snapshotJSON, contractJSON, string(run.VerificationStatus), run.StartedAt, run.ExecutionStartedAt, run.ActiveRuntimeMS, run.HeartbeatAt, run.CompletedAt, run.CreatedAt, run.UpdatedAt)
	return run, err
}

func (s *PostgresStore) UpdateRunAgent(id string, agentID string) (domain.Run, error) {
	if _, ok, err := s.GetAgent(agentID); err != nil {
		return domain.Run{}, err
	} else if !ok {
		return domain.Run{}, errors.New("agent not found")
	}
	return s.scanRunQuery(`
		UPDATE runs
		SET agent_id = $1, updated_at = $2
		WHERE id = $3
		RETURNING id, agent_id, conversation_id, status, error, runtime_snapshot, completion_contract, verification_status, started_at, execution_started_at, active_runtime_ms, heartbeat_at, completed_at, created_at, updated_at`,
		agentID, time.Now().UTC(), id)
}

func (s *PostgresStore) UpdateRunStatus(id string, status domain.RunStatus, errorMessage string) (domain.Run, error) {
	now := time.Now().UTC()
	return s.scanRunQuery(`
		UPDATE runs
		SET status = $1,
			error = $2,
			started_at = CASE WHEN $1 = 'running' AND started_at IS NULL THEN $3 ELSE started_at END,
			active_runtime_ms = active_runtime_ms + CASE
				WHEN status = 'running' AND $1 <> 'running' AND execution_started_at IS NOT NULL
				THEN GREATEST(0, FLOOR(EXTRACT(EPOCH FROM ($3 - execution_started_at)) * 1000)::bigint)
				ELSE 0
			END,
			execution_started_at = CASE
				WHEN status <> 'running' AND $1 = 'running' THEN $3
				WHEN status = 'running' AND $1 <> 'running' THEN NULL
				ELSE execution_started_at
			END,
			heartbeat_at = CASE WHEN $1 = 'running' THEN $3 ELSE heartbeat_at END,
			completed_at = CASE
				WHEN $1 = 'waiting_for_user' THEN NULL
				WHEN $1 = 'running' THEN NULL
				WHEN $1 IN ('completed', 'failed', 'failed_recoverable', 'canceled') THEN $3
				ELSE completed_at
			END,
			updated_at = $3
		WHERE id = $4
		RETURNING id, agent_id, conversation_id, status, error, runtime_snapshot, completion_contract, verification_status, started_at, execution_started_at, active_runtime_ms, heartbeat_at, completed_at, created_at, updated_at`,
		string(status), strings.TrimSpace(errorMessage), now, id)
}

func (s *PostgresStore) UpdateRunVerificationStatus(id string, status domain.VerificationStatus) (domain.Run, error) {
	return s.scanRunQuery(`
		UPDATE runs
		SET verification_status = $1, updated_at = $2
		WHERE id = $3
		RETURNING id, agent_id, conversation_id, status, error, runtime_snapshot, completion_contract, verification_status, started_at, execution_started_at, active_runtime_ms, heartbeat_at, completed_at, created_at, updated_at`,
		string(status), time.Now().UTC(), id)
}

func (s *PostgresStore) AppendVerificationRecord(record domain.VerificationRecord) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	artifactIDs, err := json.Marshal(record.Evidence.ArtifactIDs)
	if err != nil {
		return err
	}
	detailsValue := record.Evidence.Details
	if detailsValue == nil {
		detailsValue = map[string]any{}
	}
	details, err := json.Marshal(detailsValue)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`
		INSERT INTO verification_evidence (
			id, run_id, stage_id, contract_id, contract_version, verifier_id,
			verifier_type, verifier_version, attempt, subject_hash, snapshot_hash,
			status, started_at, completed_at, duration_ms, exit_code, summary,
			details, artifact_ids, supersedes_evidence_id
		) VALUES ($1,$2,NULLIF($3,''),$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,NULLIF($20,''))`,
		record.Evidence.ID, record.Evidence.RunID, record.Evidence.StageID,
		record.Evidence.ContractID, record.Evidence.ContractVersion, record.Evidence.VerifierID,
		string(record.Evidence.VerifierType), record.Evidence.VerifierVersion, record.Evidence.Attempt,
		record.Evidence.SubjectHash, record.Evidence.SnapshotHash, string(record.Evidence.Status),
		record.Evidence.StartedAt, record.Evidence.CompletedAt, record.Evidence.DurationMS,
		record.Evidence.ExitCode, record.Evidence.Summary, details, artifactIDs, record.Evidence.SupersedesEvidenceID)
	if err != nil {
		return err
	}
	for _, artifact := range record.Artifacts {
		if artifact.RunID != record.Evidence.RunID || artifact.EvidenceID != record.Evidence.ID {
			return errors.New("verification artifact does not match evidence")
		}
		if _, err := tx.Exec(`
			INSERT INTO verification_artifacts (
				id, run_id, evidence_id, kind, media_type, content, content_hash,
				byte_size, truncated, created_at
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
			artifact.ID, artifact.RunID, artifact.EvidenceID, artifact.Kind,
			artifact.MediaType, artifact.Content, artifact.ContentHash,
			artifact.ByteSize, artifact.Truncated, artifact.CreatedAt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *PostgresStore) ListVerificationEvidence(runID string) ([]domain.VerificationEvidence, error) {
	rows, err := s.db.Query(`
		SELECT id, run_id, COALESCE(stage_id,''), contract_id, contract_version,
			verifier_id, verifier_type, verifier_version, attempt, subject_hash,
			snapshot_hash, status, started_at, completed_at, duration_ms, exit_code,
			summary, details, artifact_ids, COALESCE(supersedes_evidence_id,'')
		FROM verification_evidence WHERE run_id = $1 ORDER BY started_at, id`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []domain.VerificationEvidence{}
	for rows.Next() {
		item, err := scanVerificationEvidence(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *PostgresStore) ListVerificationArtifacts(runID string) ([]domain.VerificationArtifact, error) {
	rows, err := s.db.Query(`
		SELECT id, run_id, evidence_id, kind, media_type, content, content_hash,
			byte_size, truncated, created_at
		FROM verification_artifacts WHERE run_id = $1 ORDER BY created_at, id`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []domain.VerificationArtifact{}
	for rows.Next() {
		var item domain.VerificationArtifact
		if err := rows.Scan(&item.ID, &item.RunID, &item.EvidenceID, &item.Kind,
			&item.MediaType, &item.Content, &item.ContentHash, &item.ByteSize,
			&item.Truncated, &item.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *PostgresStore) UpdateRunHeartbeat(id string) (domain.Run, error) {
	now := time.Now().UTC()
	return s.scanRunQuery(`
		UPDATE runs
		SET heartbeat_at = $1, updated_at = $1
		WHERE id = $2
		RETURNING id, agent_id, conversation_id, status, error, runtime_snapshot, completion_contract, verification_status, started_at, execution_started_at, active_runtime_ms, heartbeat_at, completed_at, created_at, updated_at`,
		now, id)
}

func (s *PostgresStore) ListStaleRunningRuns(cutoff time.Time) ([]domain.Run, error) {
	rows, err := s.db.Query(`
		SELECT id, agent_id, conversation_id, status, error, runtime_snapshot, completion_contract, verification_status, started_at, execution_started_at, active_runtime_ms, heartbeat_at, completed_at, created_at, updated_at
		FROM runs
		WHERE status = 'running'
			AND (heartbeat_at IS NULL OR heartbeat_at < $1)
		ORDER BY created_at ASC`, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []domain.Run{}
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, run)
	}
	return items, rows.Err()
}

func (s *PostgresStore) GetRun(id string) (domain.Run, bool, error) {
	run, err := s.scanRunQuery(`
		SELECT id, agent_id, conversation_id, status, error, runtime_snapshot, completion_contract, verification_status, started_at, execution_started_at, active_runtime_ms, heartbeat_at, completed_at, created_at, updated_at
		FROM runs
		WHERE id = $1`, id)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Run{}, false, nil
	}
	if err != nil {
		return domain.Run{}, false, err
	}
	return run, true, nil
}

func (s *PostgresStore) ListRuns() ([]domain.Run, error) {
	rows, err := s.db.Query(`
		SELECT id, agent_id, conversation_id, status, error, runtime_snapshot, completion_contract, verification_status, started_at, execution_started_at, active_runtime_ms, heartbeat_at, completed_at, created_at, updated_at
		FROM runs
		ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []domain.Run{}
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, run)
	}
	return items, rows.Err()
}

func (s *PostgresStore) scanRunQuery(query string, args ...any) (domain.Run, error) {
	run, err := scanRun(s.db.QueryRow(query, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Run{}, errors.New("run not found")
	}
	return run, err
}

func scanRun(row scanner) (domain.Run, error) {
	var run domain.Run
	var status string
	var errorMessage sql.NullString
	var startedAt sql.NullTime
	var executionStartedAt sql.NullTime
	var heartbeatAt sql.NullTime
	var completedAt sql.NullTime
	var snapshotJSON []byte
	var contractJSON []byte
	var verificationStatus string
	if err := row.Scan(&run.ID, &run.AgentID, &run.ConversationID, &status, &errorMessage, &snapshotJSON, &contractJSON, &verificationStatus, &startedAt, &executionStartedAt, &run.ActiveRuntimeMS, &heartbeatAt, &completedAt, &run.CreatedAt, &run.UpdatedAt); err != nil {
		return domain.Run{}, err
	}
	if len(contractJSON) > 0 && string(contractJSON) != "null" {
		var contract domain.CompletionContract
		if err := json.Unmarshal(contractJSON, &contract); err != nil {
			return domain.Run{}, err
		}
		run.CompletionContract = &contract
	}
	run.VerificationStatus = domain.VerificationStatus(verificationStatus)
	if run.VerificationStatus == "" {
		run.VerificationStatus = domain.VerificationNotRequired
	}
	if len(snapshotJSON) > 0 && string(snapshotJSON) != "null" {
		var snapshot domain.RuntimeSnapshot
		if err := json.Unmarshal(snapshotJSON, &snapshot); err != nil {
			return domain.Run{}, err
		}
		run.RuntimeSnapshot = &snapshot
	}
	run.Status = domain.RunStatus(status)
	if errorMessage.Valid {
		run.Error = errorMessage.String
	}
	if startedAt.Valid {
		run.StartedAt = &startedAt.Time
	}
	if executionStartedAt.Valid {
		run.ExecutionStartedAt = &executionStartedAt.Time
	}
	if heartbeatAt.Valid {
		run.HeartbeatAt = &heartbeatAt.Time
	}
	if completedAt.Valid {
		run.CompletedAt = &completedAt.Time
	}
	return run, nil
}

func scanVerificationEvidence(row scanner) (domain.VerificationEvidence, error) {
	var item domain.VerificationEvidence
	var verifierType string
	var status string
	var details []byte
	var artifactIDs []byte
	var exitCode sql.NullInt64
	if err := row.Scan(&item.ID, &item.RunID, &item.StageID, &item.ContractID,
		&item.ContractVersion, &item.VerifierID, &verifierType, &item.VerifierVersion,
		&item.Attempt, &item.SubjectHash, &item.SnapshotHash, &status, &item.StartedAt,
		&item.CompletedAt, &item.DurationMS, &exitCode, &item.Summary, &details, &artifactIDs,
		&item.SupersedesEvidenceID); err != nil {
		return domain.VerificationEvidence{}, err
	}
	item.VerifierType = domain.VerifierType(verifierType)
	item.Status = domain.VerificationStatus(status)
	if exitCode.Valid {
		value := int(exitCode.Int64)
		item.ExitCode = &value
	}
	item.Details = map[string]any{}
	if len(details) > 0 {
		if err := json.Unmarshal(details, &item.Details); err != nil {
			return domain.VerificationEvidence{}, err
		}
	}
	if item.Details == nil {
		item.Details = map[string]any{}
	}
	if len(artifactIDs) > 0 {
		if err := json.Unmarshal(artifactIDs, &item.ArtifactIDs); err != nil {
			return domain.VerificationEvidence{}, err
		}
	}
	return item, nil
}
