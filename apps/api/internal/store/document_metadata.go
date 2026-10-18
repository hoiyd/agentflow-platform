package store

import (
	"encoding/json"
	"fmt"
	"maps"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/failure"
	"agentflow-platform/apps/api/internal/rag/metadatafilter"
)

var (
	ErrDocumentMetadataConflict        = failure.New(failure.Definition{Message: "document changed; reload and retry with expected_updated_at", Info: failure.Info{Code: "document_metadata_conflict", Source: "knowledge_metadata", Category: failure.CategoryValidation}})
	ErrDocumentMetadataRequiresReindex = failure.New(failure.Definition{Message: "source or indexing fields cannot change through metadata update; ingest to rebuild the index", Info: failure.Info{Code: "document_metadata_requires_reindex", Source: "knowledge_metadata", Category: failure.CategoryValidation}})
	ErrInvalidDocumentMetadata         = failure.New(failure.Definition{Message: "metadata must be a non-null object of at most 64 KiB with a nonzero microsecond expected_updated_at", Info: failure.Info{Code: "invalid_document_metadata", Source: "knowledge_metadata", Category: failure.CategoryValidation}})
)

// These are existing source/structure fields, not a new business metadata schema.
// Chunk-local derived values must not be overwritten by Document inheritance.
func protectedDocumentMetadata(key string) bool {
	switch key {
	case "id", "workspace_id", "source_key", "version", "content_hash", "index_identity", "created_at", "updated_at",
		"title", "format", "filename", "chunk_index", "chunk_type", "source_format", "heading_path", "code_language", "split_reason":
		return true
	}
	return false
}

func SameDocumentIndexInputs(a, b domain.Document) bool {
	return a.Version == b.Version && a.ContentHash == b.ContentHash && a.Title == b.Title &&
		a.SourceType == b.SourceType && a.SourceURI == b.SourceURI && a.MimeType == b.MimeType &&
		SameDocumentMetadata(a.Metadata["format"], b.Metadata["format"])
}

func SameDocumentMetadata(a, b any) bool {
	return metadatafilter.EqualJSON(a, b)
}

type DocumentMetadataChange struct {
	Metadata          map[string]any
	Inherited         map[string]any
	PreviousInherited map[string]any
	UpdatedAt         time.Time
}

func PrepareDocumentMetadataChange(document domain.Document, update domain.DocumentMetadataUpdate) (DocumentMetadataChange, error) {
	if update.ExpectedUpdatedAt.IsZero() || !update.ExpectedUpdatedAt.Equal(update.ExpectedUpdatedAt.Truncate(time.Microsecond)) || update.Metadata == nil {
		return DocumentMetadataChange{}, ErrInvalidDocumentMetadata
	}
	if update.ExpectedIndexInputs != nil && !SameDocumentIndexInputs(document, *update.ExpectedIndexInputs) {
		return DocumentMetadataChange{}, ErrDocumentMetadataConflict
	}
	encoded, err := json.Marshal(update.Metadata)
	if err != nil || len(encoded) > 65536 {
		return DocumentMetadataChange{}, ErrInvalidDocumentMetadata
	}
	// Detach caller-owned maps and preserve JSON number precision like SQL ->>.
	canonical, valid := metadatafilter.CanonicalJSON(update.Metadata)
	if !valid || len(canonical) > 65536 {
		return DocumentMetadataChange{}, ErrInvalidDocumentMetadata
	}
	var metadata map[string]any
	decoder := json.NewDecoder(strings.NewReader(canonical))
	decoder.UseNumber()
	if err := decoder.Decode(&metadata); err != nil {
		return DocumentMetadataChange{}, ErrInvalidDocumentMetadata
	}
	for key, value := range metadata {
		if protectedDocumentMetadata(key) {
			original, exists := document.Metadata[key]
			if !exists || !SameDocumentMetadata(value, original) {
				return DocumentMetadataChange{}, fmt.Errorf("%w: %s", ErrDocumentMetadataRequiresReindex, key)
			}
		}
	}
	for key, value := range document.Metadata {
		if protectedDocumentMetadata(key) {
			metadata[key] = value
		}
	}
	change := DocumentMetadataChange{Metadata: metadata, Inherited: map[string]any{}, UpdatedAt: document.UpdatedAt}
	if SameDocumentMetadata(metadata, document.Metadata) {
		return change, nil
	}
	if !document.UpdatedAt.Equal(update.ExpectedUpdatedAt) {
		return DocumentMetadataChange{}, ErrDocumentMetadataConflict
	}
	change.PreviousInherited = map[string]any{}
	for key := range document.Metadata {
		if !protectedDocumentMetadata(key) {
			change.PreviousInherited[key] = document.Metadata[key]
		}
	}
	for key, value := range metadata {
		if !protectedDocumentMetadata(key) {
			change.Inherited[key] = value
		}
	}
	change.UpdatedAt = time.Now().UTC().Truncate(time.Microsecond)
	if !change.UpdatedAt.After(document.UpdatedAt) {
		change.UpdatedAt = document.UpdatedAt.Add(time.Microsecond)
	}
	return change, nil
}

func (change DocumentMetadataChange) ChunkMetadata(original map[string]any) map[string]any {
	metadata := maps.Clone(original)
	if metadata == nil {
		metadata = map[string]any{}
	}
	for key, value := range change.PreviousInherited {
		if old, exists := original[key]; exists && SameDocumentMetadata(old, value) {
			delete(metadata, key)
		}
	}
	for key, value := range change.Inherited {
		old, exists := original[key]
		prior, inherited := change.PreviousInherited[key]
		if !exists || inherited && SameDocumentMetadata(old, prior) {
			metadata[key] = value
		}
	}
	return metadata
}
