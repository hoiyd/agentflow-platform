package store

var postgresContextSchema = []string{
	`CREATE TABLE IF NOT EXISTS context_compactions (
		id text PRIMARY KEY,
		conversation_id text NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
		run_id text NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
			trigger text NOT NULL,
			status text NOT NULL DEFAULT 'completed',
			generation bigint NOT NULL DEFAULT 1,
			previous_compaction_id text NOT NULL DEFAULT '',
			replacement_summary_id text NOT NULL DEFAULT '',
			summary text NOT NULL,
			source_message_ids jsonb NOT NULL DEFAULT '[]'::jsonb,
			source_event_ids jsonb NOT NULL DEFAULT '[]'::jsonb,
			shadowed_first_message_id text NOT NULL DEFAULT '',
			shadowed_last_message_id text NOT NULL DEFAULT '',
			shadowed_message_count integer NOT NULL DEFAULT 0,
			source_hash text NOT NULL,
			before_tokens integer NOT NULL,
			after_tokens integer NOT NULL,
			target_summary_tokens integer NOT NULL DEFAULT 0,
			reduction_ratio double precision NOT NULL DEFAULT 0,
			consecutive_low_yield integer NOT NULL DEFAULT 0,
			summary_model text NOT NULL,
			algorithm_version text NOT NULL,
			surface_replaced_at timestamptz,
			created_at timestamptz NOT NULL,
			UNIQUE(conversation_id, source_hash)
		)`,
	`ALTER TABLE context_compactions ADD COLUMN IF NOT EXISTS status text NOT NULL DEFAULT 'completed'`,
	`ALTER TABLE context_compactions ADD COLUMN IF NOT EXISTS generation bigint NOT NULL DEFAULT 1`,
	`ALTER TABLE context_compactions ADD COLUMN IF NOT EXISTS previous_compaction_id text NOT NULL DEFAULT ''`,
	`ALTER TABLE context_compactions ADD COLUMN IF NOT EXISTS replacement_summary_id text NOT NULL DEFAULT ''`,
	`ALTER TABLE context_compactions ADD COLUMN IF NOT EXISTS source_event_ids jsonb NOT NULL DEFAULT '[]'::jsonb`,
	`ALTER TABLE context_compactions ADD COLUMN IF NOT EXISTS shadowed_first_message_id text NOT NULL DEFAULT ''`,
	`ALTER TABLE context_compactions ADD COLUMN IF NOT EXISTS shadowed_last_message_id text NOT NULL DEFAULT ''`,
	`ALTER TABLE context_compactions ADD COLUMN IF NOT EXISTS shadowed_message_count integer NOT NULL DEFAULT 0`,
	`ALTER TABLE context_compactions ADD COLUMN IF NOT EXISTS target_summary_tokens integer NOT NULL DEFAULT 0`,
	`ALTER TABLE context_compactions ADD COLUMN IF NOT EXISTS reduction_ratio double precision NOT NULL DEFAULT 0`,
	`ALTER TABLE context_compactions ADD COLUMN IF NOT EXISTS consecutive_low_yield integer NOT NULL DEFAULT 0`,
	`ALTER TABLE context_compactions ADD COLUMN IF NOT EXISTS surface_replaced_at timestamptz`,
	`UPDATE context_compactions SET replacement_summary_id = 'summary:' || id WHERE replacement_summary_id = ''`,
	`DO $$ BEGIN
			IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'context_compactions_generation_idx') THEN
				WITH ranked AS (
					SELECT id, ROW_NUMBER() OVER (PARTITION BY conversation_id ORDER BY created_at, id) AS generation
					FROM context_compactions
				) UPDATE context_compactions SET generation = ranked.generation FROM ranked WHERE context_compactions.id = ranked.id;
			END IF;
		END $$`,
	`WITH ordered AS (
			SELECT id, LAG(id) OVER (PARTITION BY conversation_id ORDER BY generation, created_at, id) AS previous_id
			FROM context_compactions
		) UPDATE context_compactions
		SET previous_compaction_id = COALESCE(ordered.previous_id, '')
		FROM ordered
		WHERE context_compactions.id = ordered.id AND context_compactions.previous_compaction_id = ''`,
	`UPDATE context_compactions
		SET shadowed_first_message_id = COALESCE(source_message_ids->>0, ''),
			shadowed_last_message_id = COALESCE(source_message_ids->>(jsonb_array_length(source_message_ids)-1), ''),
			shadowed_message_count = jsonb_array_length(source_message_ids)
		WHERE shadowed_message_count = 0 AND jsonb_array_length(source_message_ids) > 0`,
	`CREATE UNIQUE INDEX IF NOT EXISTS context_compactions_generation_idx ON context_compactions(conversation_id, generation)`,
	`CREATE INDEX IF NOT EXISTS context_compactions_active_idx ON context_compactions(conversation_id, generation DESC) WHERE status = 'completed'`,
	`CREATE TABLE IF NOT EXISTS model_request_records (
		id text PRIMARY KEY,
		run_id text NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
		conversation_id text NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
		stage_id text,
		turn_id text,
		model_call_id text NOT NULL,
		attempt integer NOT NULL,
		operation text NOT NULL,
		provider text NOT NULL,
		model text NOT NULL,
		context_manifest_id text,
		runtime_snapshot_hash text NOT NULL,
		payload_hash text NOT NULL,
		payload_bytes integer NOT NULL,
		parameters jsonb NOT NULL DEFAULT '{}'::jsonb,
		source_token_breakdown jsonb NOT NULL DEFAULT '{}'::jsonb,
		message_count integer NOT NULL,
		tool_count integer NOT NULL,
		capture_mode text NOT NULL,
		capture_content text NOT NULL DEFAULT '',
		capture_content_hash text NOT NULL DEFAULT '',
		capture_original_bytes integer NOT NULL,
		capture_stored_bytes integer NOT NULL,
		capture_redacted boolean NOT NULL DEFAULT false,
		capture_redaction_strategy text NOT NULL DEFAULT '',
		capture_redaction_count integer NOT NULL DEFAULT 0,
		capture_truncated boolean NOT NULL DEFAULT false,
		capture_reconstructable boolean NOT NULL DEFAULT false,
		capture_expires_at timestamptz,
		capture_expired boolean NOT NULL DEFAULT false,
		created_at timestamptz NOT NULL,
		UNIQUE(run_id, model_call_id, attempt)
	)`,
	`ALTER TABLE model_request_records ADD COLUMN IF NOT EXISTS source_token_breakdown jsonb NOT NULL DEFAULT '{}'::jsonb`,
	`ALTER TABLE model_request_records ADD COLUMN IF NOT EXISTS capture_redaction_strategy text NOT NULL DEFAULT ''`,
	`ALTER TABLE model_request_records ADD COLUMN IF NOT EXISTS capture_redaction_count integer NOT NULL DEFAULT 0`,
	`ALTER TABLE model_request_records ADD COLUMN IF NOT EXISTS capture_expires_at timestamptz`,
	`ALTER TABLE model_request_records ADD COLUMN IF NOT EXISTS capture_expired boolean NOT NULL DEFAULT false`,
	`CREATE INDEX IF NOT EXISTS model_request_records_run_created_idx ON model_request_records(run_id, created_at, id)`,
}
