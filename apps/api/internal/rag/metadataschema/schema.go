package metadataschema

import "agentflow-platform/apps/api/internal/domain"

const Version = "knowledge-metadata-v1"

// Describe returns detached definitions; callers cannot mutate the write contract.
func Describe() domain.DocumentMetadataSchema {
	min, max := 0.0, 100.0
	fields := []domain.DocumentMetadataField{
		{Key: "product", Type: "string", MaxLength: 120},
		{Key: "environment", Type: "enum", Values: []string{"development", "test", "staging", "production"}},
		{Key: "language", Type: "string", MaxLength: 35},
		{Key: "tags", Type: "string-array", MaxLength: 64, MaxItems: 32},
		{Key: "priority", Type: "number", Minimum: &min, Maximum: &max},
		{Key: "is_reference", Type: "bool"},
		{Key: "published_at", Type: "date-time"},
	}
	for i := range fields {
		fields[i].Owner, fields[i].Scope = "owner_declared", "both"
	}
	for _, key := range []string{"id", "workspace_id", "source_key", "version", "content_hash", "index_identity", "created_at", "updated_at", "metadata_schema_version", "title", "format", "filename", "chunk_index", "chunk_type", "source_format", "heading_path", "code_language", "split_reason"} {
		scope := "document"
		switch key {
		case "title", "format", "filename":
			scope = "both"
		case "chunk_index", "chunk_type", "source_format", "heading_path", "code_language", "split_reason":
			scope = "chunk"
		}
		fields = append(fields, domain.DocumentMetadataField{Key: key, Type: "derived", Owner: "system_derived", Scope: scope})
	}
	return domain.DocumentMetadataSchema{Version: Version, Fields: fields, ExtensionPolicy: "preserve_unverified", TypedFiltering: false}
}

func IsSystemField(key string) bool {
	for _, field := range Describe().Fields {
		if field.Key == key {
			return field.Owner == "system_derived"
		}
	}
	return false
}
