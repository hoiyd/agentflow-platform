package store

import (
	"context"
	"fmt"
)

type postgresColumnRequirement struct {
	Table   string
	Column  string
	UDTName string
	NotNull bool
}

var postgresRequiredColumns = []postgresColumnRequirement{
	{Table: "conversations", Column: "workspace_id", UDTName: "text", NotNull: true},
	{Table: "messages", Column: "workspace_id", UDTName: "text", NotNull: true},
	{Table: "messages", Column: "citations", UDTName: "jsonb", NotNull: true},
	{Table: "agents", Column: "memory_enabled", NotNull: true},
	{Table: "agents", Column: "retrieval_enabled", NotNull: true},
	{Table: "agents", Column: "executor", NotNull: true},
	{Table: "agents", Column: "deleted_at"},
	{Table: "runs", Column: "workspace_id", UDTName: "text", NotNull: true},
	{Table: "runs", Column: "heartbeat_at"},
	{Table: "runs", Column: "runtime_snapshot"},
	{Table: "runs", Column: "completion_contract"},
	{Table: "runs", Column: "verification_status", NotNull: true},
	{Table: "runs", Column: "execution_started_at"},
	{Table: "runs", Column: "active_runtime_ms", NotNull: true},
	{Table: "stage_checkpoints", Column: "provider", UDTName: "text", NotNull: true},
	{Table: "stage_checkpoints", Column: "status", UDTName: "text", NotNull: true},
	{Table: "stage_checkpoints", Column: "event_cursor", UDTName: "int8", NotNull: true},
	{Table: "tool_effects", Column: "idempotency_key", UDTName: "text", NotNull: true},
	{Table: "tool_effects", Column: "status", UDTName: "text", NotNull: true},
	{Table: "tool_effects", Column: "result", UDTName: "bytea"},
	{Table: "verification_evidence", Column: "details", UDTName: "jsonb", NotNull: true},
	{Table: "memories", Column: "workspace_id", UDTName: "text", NotNull: true},
	{Table: "memory_candidates", Column: "confidence", UDTName: "float8", NotNull: true},
	{Table: "memory_embeddings", Column: "embedding", UDTName: "vector", NotNull: true},
	{Table: "documents", Column: "workspace_id", UDTName: "text", NotNull: true},
	{Table: "documents", Column: "version", UDTName: "text", NotNull: true},
	{Table: "documents", Column: "content_hash", UDTName: "text", NotNull: true},
	{Table: "documents", Column: "lexical_vector", UDTName: "tsvector"},
	{Table: "document_chunks", Column: "parent_id", UDTName: "text", NotNull: true},
	{Table: "document_chunks", Column: "section_path", UDTName: "jsonb", NotNull: true},
	{Table: "document_chunks", Column: "lexical_vector", UDTName: "tsvector"},
	{Table: "document_chunk_embeddings", Column: "embedding", UDTName: "vector", NotNull: true},
	{Table: "run_usage_entries", Column: "operation_id", UDTName: "text", NotNull: true},
	{Table: "run_usage_entries", Column: "purpose", UDTName: "text", NotNull: true},
	{Table: "run_usage_entries", Column: "model", UDTName: "text", NotNull: true},
	{Table: "run_usage_entries", Column: "tool_name", UDTName: "text", NotNull: true},
	{Table: "model_request_records", Column: "model_call_id", UDTName: "text", NotNull: true},
	{Table: "model_request_records", Column: "payload_hash", UDTName: "text", NotNull: true},
	{Table: "model_request_records", Column: "parameters", UDTName: "jsonb", NotNull: true},
	{Table: "model_request_records", Column: "source_token_breakdown", UDTName: "jsonb", NotNull: true},
	{Table: "model_request_records", Column: "capture_content", UDTName: "text", NotNull: true},
	{Table: "model_request_records", Column: "capture_expired", UDTName: "bool", NotNull: true},
}

func (s *PostgresStore) validateSchema(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `
		SELECT table_name, column_name, udt_name, is_nullable
		FROM information_schema.columns
		WHERE table_schema = current_schema()`)
	if err != nil {
		return fmt.Errorf("inspect postgres schema: %w", err)
	}
	defer rows.Close()

	type columnState struct {
		UDTName  string
		Nullable string
	}
	columns := map[string]columnState{}
	for rows.Next() {
		var table, column string
		var state columnState
		if err := rows.Scan(&table, &column, &state.UDTName, &state.Nullable); err != nil {
			return fmt.Errorf("scan postgres schema: %w", err)
		}
		columns[table+"."+column] = state
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("inspect postgres schema: %w", err)
	}

	for _, requirement := range postgresRequiredColumns {
		key := requirement.Table + "." + requirement.Column
		state, ok := columns[key]
		if !ok {
			return fmt.Errorf("postgres schema is missing required column %s", key)
		}
		if requirement.UDTName != "" && state.UDTName != requirement.UDTName {
			return fmt.Errorf("postgres column %s has type %s, want %s", key, state.UDTName, requirement.UDTName)
		}
		if requirement.NotNull && state.Nullable != "NO" {
			return fmt.Errorf("postgres column %s must be NOT NULL", key)
		}
	}
	return nil
}
