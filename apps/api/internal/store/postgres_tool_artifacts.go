package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"agentflow-platform/apps/api/internal/domain"
)

func (s *PostgresStore) purgeExpiredToolArtifactContent(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `UPDATE tool_artifacts SET content = ''::bytea WHERE expires_at <= now() AND octet_length(content) > 0`)
	return err
}

func (s *PostgresStore) CreateToolArtifact(artifact domain.ToolArtifact, content []byte) (domain.ToolArtifact, error) {
	if err := validateToolArtifact(artifact, content); err != nil {
		return domain.ToolArtifact{}, err
	}
	_, err := s.db.Exec(`
		INSERT INTO tool_artifacts (
			id, schema_version, run_id, stage_id, turn_id, tool_call_id, tool_name,
			definition_revision, media_type, content_hash, original_byte_size,
			stored_byte_size, redacted, redaction_strategy, redaction_count,
			content, created_at, expires_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)
		ON CONFLICT (id) DO NOTHING`,
		artifact.ID, artifact.SchemaVersion, artifact.RunID, artifact.StageID, artifact.TurnID,
		artifact.ToolCallID, artifact.ToolName, artifact.DefinitionRevision, artifact.MediaType,
		artifact.ContentHash, artifact.OriginalByteSize, artifact.StoredByteSize, artifact.Redacted,
		artifact.RedactionStrategy, artifact.RedactionCount, content, artifact.CreatedAt, artifact.ExpiresAt,
	)
	if err != nil {
		return domain.ToolArtifact{}, err
	}
	existing, existingContent, ok, err := s.getToolArtifact(artifact.RunID, artifact.ID)
	if err != nil {
		return domain.ToolArtifact{}, err
	}
	if !ok {
		return domain.ToolArtifact{}, errors.New("tool artifact was not persisted")
	}
	if existing.ContentHash != artifact.ContentHash || existing.ToolCallID != artifact.ToolCallID || existing.StoredByteSize != artifact.StoredByteSize {
		return domain.ToolArtifact{}, errors.New("tool artifact idempotency conflict")
	}
	if len(existingContent) != existing.StoredByteSize || toolArtifactContentHash(existingContent) != existing.ContentHash {
		return domain.ToolArtifact{}, errors.New("existing tool artifact content is unavailable or corrupt")
	}
	return existing, nil
}

func (s *PostgresStore) ListToolArtifacts(runID string) ([]domain.ToolArtifact, error) {
	if _, ok, err := s.GetRun(runID); err != nil {
		return nil, err
	} else if !ok {
		return nil, ErrNotFound("run")
	}
	rows, err := s.db.Query(`SELECT `+toolArtifactColumns+` FROM tool_artifacts WHERE run_id = $1 ORDER BY created_at, id`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]domain.ToolArtifact, 0)
	for rows.Next() {
		artifact, err := scanToolArtifact(rows)
		if err != nil {
			return nil, err
		}
		artifact.Expired = toolArtifactExpired(artifact, time.Now().UTC())
		result = append(result, artifact)
	}
	return result, rows.Err()
}

func (s *PostgresStore) ReadToolArtifact(runID string, artifactID string, offset int, limit int) (domain.ToolArtifactRead, error) {
	offset, limit, err := normalizeArtifactRead(offset, limit)
	if err != nil {
		return domain.ToolArtifactRead{}, err
	}
	artifact, content, ok, err := s.getToolArtifact(runID, artifactID)
	if err != nil {
		return domain.ToolArtifactRead{}, err
	}
	if !ok {
		return domain.ToolArtifactRead{}, ErrNotFound("tool artifact")
	}
	if toolArtifactExpired(artifact, time.Now().UTC()) {
		return domain.ToolArtifactRead{}, ErrToolArtifactExpired
	}
	if offset > len(content) {
		return domain.ToolArtifactRead{}, ErrToolArtifactRange
	}
	end := min(len(content), offset+limit)
	return domain.ToolArtifactRead{
		Artifact: artifact, Offset: offset, Content: string(content[offset:end]),
		NextOffset: end, Complete: end == len(content),
	}, nil
}

func (s *PostgresStore) SearchToolArtifact(runID string, artifactID string, query string, maxMatches int) (domain.ToolArtifactSearchResult, error) {
	query, maxMatches, err := normalizeArtifactSearch(query, maxMatches)
	if err != nil {
		return domain.ToolArtifactSearchResult{}, err
	}
	artifact, content, ok, err := s.getToolArtifact(runID, artifactID)
	if err != nil {
		return domain.ToolArtifactSearchResult{}, err
	}
	if !ok {
		return domain.ToolArtifactSearchResult{}, ErrNotFound("tool artifact")
	}
	if toolArtifactExpired(artifact, time.Now().UTC()) {
		return domain.ToolArtifactSearchResult{}, ErrToolArtifactExpired
	}
	return searchToolArtifact(artifact, content, query, maxMatches), nil
}

const toolArtifactColumns = `id, schema_version, run_id, stage_id, turn_id,
	tool_call_id, tool_name, definition_revision, media_type, content_hash,
	original_byte_size, stored_byte_size, redacted, redaction_strategy,
	redaction_count, created_at, expires_at`

func (s *PostgresStore) getToolArtifact(runID string, artifactID string) (domain.ToolArtifact, []byte, bool, error) {
	row := s.db.QueryRow(`SELECT `+toolArtifactColumns+`, content FROM tool_artifacts WHERE run_id = $1 AND id = $2`, runID, artifactID)
	artifact, content, err := scanToolArtifactWithContent(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ToolArtifact{}, nil, false, nil
	}
	return artifact, content, err == nil, err
}

func scanToolArtifact(row scanner) (domain.ToolArtifact, error) {
	var artifact domain.ToolArtifact
	var expiresAt sql.NullTime
	err := row.Scan(
		&artifact.ID, &artifact.SchemaVersion, &artifact.RunID, &artifact.StageID, &artifact.TurnID,
		&artifact.ToolCallID, &artifact.ToolName, &artifact.DefinitionRevision, &artifact.MediaType,
		&artifact.ContentHash, &artifact.OriginalByteSize, &artifact.StoredByteSize, &artifact.Redacted,
		&artifact.RedactionStrategy, &artifact.RedactionCount, &artifact.CreatedAt, &expiresAt,
	)
	if expiresAt.Valid {
		artifact.ExpiresAt = &expiresAt.Time
	}
	return artifact, err
}

func scanToolArtifactWithContent(row scanner) (domain.ToolArtifact, []byte, error) {
	var artifact domain.ToolArtifact
	var expiresAt sql.NullTime
	var content []byte
	err := row.Scan(
		&artifact.ID, &artifact.SchemaVersion, &artifact.RunID, &artifact.StageID, &artifact.TurnID,
		&artifact.ToolCallID, &artifact.ToolName, &artifact.DefinitionRevision, &artifact.MediaType,
		&artifact.ContentHash, &artifact.OriginalByteSize, &artifact.StoredByteSize, &artifact.Redacted,
		&artifact.RedactionStrategy, &artifact.RedactionCount, &artifact.CreatedAt, &expiresAt, &content,
	)
	if expiresAt.Valid {
		artifact.ExpiresAt = &expiresAt.Time
	}
	return artifact, content, err
}
