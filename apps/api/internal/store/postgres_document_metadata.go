package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"agentflow-platform/apps/api/internal/domain"
)

func (s *PostgresStore) GetDocumentBySource(workspaceID, sourceKey string) (domain.Document, bool, error) {
	return getDocumentBySource(s.db, NormalizeWorkspaceID(workspaceID), strings.TrimSpace(sourceKey))
}

func (s *PostgresStore) UpdateDocumentMetadata(workspaceID, id string, update domain.DocumentMetadataUpdate) (domain.Document, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return domain.Document{}, err
	}
	defer tx.Rollback()
	workspaceID = NormalizeWorkspaceID(workspaceID)
	// Share the row lock with source replacement/deletion. Read after acquiring it,
	// so a competing metadata edit is checked against its committed token.
	var locked string
	err = tx.QueryRow(`SELECT id FROM documents WHERE id=$1 AND workspace_id=$2 FOR UPDATE`, strings.TrimSpace(id), workspaceID).Scan(&locked)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Document{}, ErrNotFound("document")
	}
	if err != nil {
		return domain.Document{}, err
	}
	document, err := scanDocument(tx.QueryRow(`SELECT d.id,d.workspace_id,d.source_key,d.title,d.version,d.content_hash,
		d.chunker_version,d.embedding_provider,d.embedding_model,d.embedding_dimensions,
		d.source_type,d.source_uri,d.mime_type,d.metadata,d.created_at,d.updated_at,
		(SELECT COUNT(*) FROM document_chunks c WHERE c.document_id=d.id),
		(SELECT COUNT(*) FROM document_chunk_embeddings e JOIN document_chunks c ON c.id=e.chunk_id WHERE c.document_id=d.id)
		FROM documents d WHERE d.id=$1 AND d.workspace_id=$2`, locked, workspaceID))
	if err != nil {
		return domain.Document{}, err
	}
	change, err := PrepareDocumentMetadataChange(document, update)
	if err != nil {
		return domain.Document{}, err
	}
	if change.UpdatedAt.Equal(document.UpdatedAt) {
		return document, nil
	}
	metadata, _ := json.Marshal(change.Metadata)
	inherited, _ := json.Marshal(change.Inherited)
	previous, _ := json.Marshal(change.PreviousInherited)
	if _, err = tx.Exec(`UPDATE documents SET metadata=$1,updated_at=$2 WHERE id=$3 AND workspace_id=$4`, metadata, change.UpdatedAt, document.ID, workspaceID); err != nil {
		return domain.Document{}, err
	}
	// Replace only values inherited from the prior Document. Distinct Chunk-local
	// overrides survive; JSON text comparison keeps decimal scale significant.
	if _, err = tx.Exec(`UPDATE document_chunks c SET metadata=(c.metadata - ARRAY(
		SELECT prior.key FROM jsonb_each($1::jsonb) prior
		WHERE (c.metadata -> prior.key)::text = prior.value::text
	)) || COALESCE((SELECT jsonb_object_agg(incoming.key,incoming.value)
		FROM jsonb_each($2::jsonb) incoming
		WHERE NOT (c.metadata ? incoming.key)
			OR (c.metadata -> incoming.key)::text = ($1::jsonb -> incoming.key)::text
	),'{}'::jsonb) WHERE c.document_id=$3`, previous, inherited, document.ID); err != nil {
		return domain.Document{}, err
	}
	if err = tx.Commit(); err != nil {
		return domain.Document{}, err
	}
	document.Metadata, document.UpdatedAt = change.Metadata, change.UpdatedAt
	return document, nil
}
