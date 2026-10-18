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
		d.source_type,d.source_uri,d.mime_type,d.metadata,d.created_at,d.updated_at,d.metadata_schema_version,
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
	if _, err = tx.Exec(`UPDATE documents SET metadata=$1,updated_at=$2,metadata_schema_version=$5 WHERE id=$3 AND workspace_id=$4`, metadata, change.UpdatedAt, document.ID, workspaceID, change.SchemaVersion); err != nil {
		return domain.Document{}, err
	}
	if err := updateDocumentChunkMetadata(tx, document.ID, change); err != nil {
		return domain.Document{}, err
	}
	if err = tx.Commit(); err != nil {
		return domain.Document{}, err
	}
	document.Metadata, document.UpdatedAt, document.MetadataSchemaVersion = change.Metadata, change.UpdatedAt, change.SchemaVersion
	return document, nil
}

func updateDocumentChunkMetadata(tx *sql.Tx, documentID string, change DocumentMetadataChange) error {
	// Legacy maps need no typed validation; retain their set-based update without
	// copying every Chunk's inherited payload across the database connection.
	if change.SchemaVersion == "" {
		previous, _ := json.Marshal(change.PreviousInherited)
		inherited, _ := json.Marshal(change.Inherited)
		_, err := tx.Exec(`UPDATE document_chunks c SET metadata=(c.metadata - ARRAY(
			SELECT prior.key FROM jsonb_each($1::jsonb) prior
			WHERE (c.metadata -> prior.key)::text = prior.value::text
		)) || COALESCE((SELECT jsonb_object_agg(incoming.key,incoming.value)
			FROM jsonb_each($2::jsonb) incoming
			WHERE NOT (c.metadata ? incoming.key)
				OR (c.metadata -> incoming.key)::text = ($1::jsonb -> incoming.key)::text
		),'{}'::jsonb) WHERE c.document_id=$3`, previous, inherited, documentID)
		return err
	}
	// Validate effective local overrides before upgrading a legacy Document.
	// Prepare detached replacements, then write them in one statement/transaction.
	rows, err := tx.Query(`SELECT id,metadata FROM document_chunks WHERE document_id=$1`, documentID)
	if err != nil {
		return err
	}
	patches := map[string]map[string]any{}
	for rows.Next() {
		var id string
		var raw []byte
		var original map[string]any
		if err = rows.Scan(&id, &raw); err == nil {
			err = decodeDocumentMetadata(raw, &original)
		}
		if err == nil {
			patches[id], err = change.ValidatedChunkMetadata(original)
		}
		if err != nil {
			rows.Close()
			return err
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	chunkMetadata, err := json.Marshal(patches)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`UPDATE document_chunks c SET metadata=patch.value
		FROM jsonb_each($1::jsonb) patch WHERE c.id=patch.key AND c.document_id=$2`, chunkMetadata, documentID)
	return err
}
