package store

import (
	"strings"
	"testing"
)

func TestMCPConnectionSchemaIsInstalledOnExistingDatabases(t *testing.T) {
	joined := strings.Join(postgresMigrations, "\n")
	for _, required := range []string{"CREATE TABLE IF NOT EXISTS mcp_connections", "credential_ciphertext text NOT NULL DEFAULT ''", "discovery jsonb NOT NULL DEFAULT '[]'", "mcp_connections_workspace_idx"} {
		if !strings.Contains(joined, required) {
			t.Fatalf("missing MCP schema step %s", required)
		}
	}
	if !strings.Contains(strings.Join(workspaceReferenceTables, ","), "mcp_connections") {
		t.Fatal("MCP connections bypass Workspace lifecycle guards")
	}
}

func TestPostgresMigrationsUpgradeLegacyRunUsageEntries(t *testing.T) {
	joined := strings.Join(postgresMigrations, "\n")
	for _, expected := range []string{
		"ADD COLUMN IF NOT EXISTS operation_id text",
		"SET operation_id = id",
		"ALTER COLUMN operation_id SET NOT NULL",
		"ADD COLUMN IF NOT EXISTS purpose text",
		"SET purpose = 'primary'",
		"ALTER COLUMN purpose SET NOT NULL",
		"ADD COLUMN IF NOT EXISTS model text",
		"SET model = '' WHERE model IS NULL",
		"ADD COLUMN IF NOT EXISTS tool_name text",
		"ALTER TABLE run_usage_entries ADD COLUMN IF NOT EXISTS breakdown jsonb NOT NULL DEFAULT 'null'::jsonb",
		"ALTER TABLE run_usage_entries ADD COLUMN IF NOT EXISTS cost_details jsonb NOT NULL DEFAULT 'null'::jsonb",
		"SET tool_name = '' WHERE tool_name IS NULL",
		"run_usage_entries_run_operation_kind_idx",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("missing legacy run usage migration step %q", expected)
		}
	}
}

func TestPostgresMigrationsAddAgentRoutingHints(t *testing.T) {
	joined := strings.Join(postgresMigrations, "\n")
	for _, expected := range []string{
		"routing_hints jsonb NOT NULL DEFAULT '{}'::jsonb",
		"ALTER TABLE agents ADD COLUMN IF NOT EXISTS routing_hints",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("missing Agent routing hints migration step %q", expected)
		}
	}
}

func TestPostgresMigrationsAddTrustedSkillBindings(t *testing.T) {
	joined := strings.Join(postgresMigrations, "\n")
	for _, expected := range []string{"skills jsonb NOT NULL DEFAULT '[]'::jsonb", "ALTER TABLE agents ADD COLUMN IF NOT EXISTS skills", "skill_policies jsonb NOT NULL DEFAULT '{}'::jsonb", "ALTER TABLE agents ADD COLUMN IF NOT EXISTS skill_policies"} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("missing Skill binding schema step %q", expected)
		}
	}
}

func TestPostgresMigrationsAddDurableRecoveryState(t *testing.T) {
	joined := strings.Join(postgresMigrations, "\n")
	for _, expected := range []string{
		"CREATE TABLE IF NOT EXISTS stage_checkpoints",
		"UNIQUE(run_id, stage_id)",
		"stage_checkpoints_run_cursor_idx",
		"CREATE TABLE IF NOT EXISTS tool_effects",
		"idempotency_key text PRIMARY KEY",
		"ADD COLUMN IF NOT EXISTS version bigint NOT NULL DEFAULT 1",
		"ADD COLUMN IF NOT EXISTS definition_revision text NOT NULL DEFAULT ''",
		"tool_effects_run_stage_idx",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("missing durable recovery migration step %q", expected)
		}
	}
}

func TestPostgresMigrationsRetireChildRunsAfterPreservingWorkerOutput(t *testing.T) {
	joined := strings.Join(postgresMigrations, "\n")
	for _, expected := range []string{
		"SET output = child_step.output",
		"SET status = child_checkpoint.status",
		"UPDATE tool_effects item",
		"UPDATE tool_artifacts item",
		"UPDATE model_request_records item",
		"UPDATE run_usage_entries item",
		"WITH child_events AS",
		"SET run_id = child_events.parent_run_id",
		"DELETE FROM runs WHERE id IN (SELECT child_run_id FROM run_delegations)",
		"DROP TABLE run_delegations",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("missing child Run retirement step %q", expected)
		}
	}
	if strings.Contains(joined, "CREATE TABLE IF NOT EXISTS run_delegations") {
		t.Fatal("fresh databases must not recreate the retired delegation table")
	}
}

func TestPostgresMigrationsAddStructuredTaskState(t *testing.T) {
	joined := strings.Join(postgresMigrations, "\n")
	for _, expected := range []string{
		"CREATE TABLE IF NOT EXISTS task_state_revisions",
		"UNIQUE(conversation_id, version)",
		"task_state_revisions_conversation_version_idx",
		"patch jsonb NOT NULL",
		"state jsonb NOT NULL",
		"source jsonb NOT NULL",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("missing structured task state migration step %q", expected)
		}
	}
}

func TestPostgresMigrationsRepairLegacyVectorAndConfidenceSchema(t *testing.T) {
	joined := strings.Join(postgresMigrations, "\n")
	for _, expected := range []string{
		"memory_candidates ADD COLUMN IF NOT EXISTS confidence",
		"table_name = 'memory_embeddings'",
		"table_name = 'document_chunk_embeddings'",
		"ALTER COLUMN embedding TYPE vector(1536)",
		"USING embedding::vector(1536)",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("missing legacy vector migration step %q", expected)
		}
	}
}

func TestPostgresRequiredSchemaCoversRuntimeColumns(t *testing.T) {
	columns := map[string]bool{}
	for _, requirement := range postgresRequiredColumns {
		columns[requirement.Table+"."+requirement.Column] = true
	}
	for _, expected := range []string{
		"conversations.workspace_id",
		"messages.workspace_id",
		"messages.citations",
		"messages.web_citations",
		"runs.workspace_id",
		"stage_checkpoints.provider",
		"stage_checkpoints.status",
		"stage_checkpoints.event_cursor",
		"tool_effects.idempotency_key",
		"tool_effects.status",
		"tool_effects.result",
		"memory_candidates.confidence",
		"memory_embeddings.embedding",
		"documents.workspace_id",
		"documents.source_key",
		"documents.chunker_version",
		"documents.embedding_provider",
		"documents.embedding_model",
		"documents.embedding_dimensions",
		"documents.lexical_vector",
		"document_chunks.parent_id",
		"document_chunk_embeddings.embedding",
		"run_usage_entries.operation_id",
		"run_usage_entries.purpose",
		"model_request_records.model_call_id",
		"model_request_records.payload_hash",
		"model_request_records.parameters",
		"model_request_records.source_token_breakdown",
		"model_request_records.capture_content",
		"model_request_records.capture_expired",
		"task_state_revisions.workspace_id",
		"task_state_revisions.conversation_id",
		"task_state_revisions.version",
		"task_state_revisions.patch",
		"task_state_revisions.state",
		"task_state_revisions.source",
	} {
		if !columns[expected] {
			t.Fatalf("required schema does not validate %s", expected)
		}
	}
}

func TestPostgresMigrationsAddModelRequestRecords(t *testing.T) {
	joined := strings.Join(postgresMigrations, "\n")
	for _, expected := range []string{
		"CREATE TABLE IF NOT EXISTS model_request_records",
		"UNIQUE(run_id, model_call_id, attempt)",
		"model_request_records_run_created_idx",
		"capture_reconstructable boolean",
		"source_token_breakdown jsonb",
		"capture_expires_at timestamptz",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("missing model request migration step %q", expected)
		}
	}
}

func TestPostgresMigrationsAddMetadataSchema(t *testing.T) {
	joined := strings.Join(postgresMigrations, "\n")
	if !strings.Contains(joined, "metadata_schema_version text NOT NULL DEFAULT ''") ||
		!strings.Contains(joined, "documents ADD COLUMN IF NOT EXISTS metadata_schema_version") {
		t.Fatal("document metadata schema needs both fresh and existing-database definitions")
	}
}

func TestPostgresMigrationsAddDocumentSourceTraceability(t *testing.T) {
	joined := strings.Join(postgresMigrations, "\n")
	for _, expected := range []string{
		"documents ADD COLUMN IF NOT EXISTS version",
		"documents ADD COLUMN IF NOT EXISTS content_hash",
		"document_chunks ADD COLUMN IF NOT EXISTS parent_id",
		"document_chunks ADD COLUMN IF NOT EXISTS section_path",
		"document_chunks ADD COLUMN IF NOT EXISTS start_offset",
		"document_chunks ADD COLUMN IF NOT EXISTS end_offset",
		"document_chunks ADD COLUMN IF NOT EXISTS document_version",
		"document_chunks ADD COLUMN IF NOT EXISTS content_hash",
		"idx_document_chunks_parent_index",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("missing document source migration step %q", expected)
		}
	}
}

func TestPostgresMigrationsAddDocumentIndexLifecycle(t *testing.T) {
	joined := strings.Join(postgresMigrations, "\n")
	for _, expected := range []string{
		"documents ADD COLUMN IF NOT EXISTS source_key",
		"OCTET_LENGTH(BTRIM(source_uri)) BETWEEN 1 AND 512",
		"documents ADD COLUMN IF NOT EXISTS chunker_version",
		"documents ADD COLUMN IF NOT EXISTS embedding_provider",
		"documents ADD COLUMN IF NOT EXISTS embedding_model",
		"documents ADD COLUMN IF NOT EXISTS embedding_dimensions",
		"PARTITION BY workspace_id, source_key",
		"documents_workspace_source_key_idx",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("missing document lifecycle migration step %q", expected)
		}
	}
}

func TestPostgresMigrationsAddMessageCitations(t *testing.T) {
	joined := strings.Join(postgresMigrations, "\n")
	for _, expected := range []string{
		"citations jsonb NOT NULL DEFAULT '[]'::jsonb",
		"messages ADD COLUMN IF NOT EXISTS citations",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("missing message citation migration step %q", expected)
		}
	}
}

func TestPostgresMigrationsIndexConversationEventHistory(t *testing.T) {
	joined := strings.Join(postgresMigrations, "\n")
	if !strings.Contains(joined, "idx_run_events_conversation_timestamp") {
		t.Fatal("missing source-aware session history event index")
	}
}
