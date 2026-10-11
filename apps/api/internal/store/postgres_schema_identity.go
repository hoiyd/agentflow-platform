package store

var postgresIdentitySchema = []string{
	`CREATE EXTENSION IF NOT EXISTS vector`,
	`CREATE TABLE IF NOT EXISTS auth_users (
		id text PRIMARY KEY,
		issuer text NOT NULL,
		subject text NOT NULL,
		name text NOT NULL DEFAULT '',
		UNIQUE (issuer,subject)
	)`,
	workspaceEntitySchema,
	`CREATE TABLE IF NOT EXISTS workspace_tool_config (
		workspace_id bigint PRIMARY KEY REFERENCES workspaces(id),
		allowed_tools jsonb NOT NULL DEFAULT '[]' CHECK(jsonb_typeof(allowed_tools)='array'),
		revision bigint NOT NULL DEFAULT 1,
		updated_at timestamptz NOT NULL DEFAULT NOW()
	)`,
	`CREATE TABLE IF NOT EXISTS auth_memberships (
		user_id text NOT NULL REFERENCES auth_users(id) ON DELETE CASCADE,
		workspace_id bigint NOT NULL,
		PRIMARY KEY (user_id,workspace_id)
	)`,
	`CREATE TABLE IF NOT EXISTS auth_personal_workspaces (
		user_id text PRIMARY KEY REFERENCES auth_users(id) ON DELETE CASCADE,
		workspace_id bigint UNIQUE,
		created_at timestamptz NOT NULL DEFAULT NOW()
	)`,
	`CREATE TABLE IF NOT EXISTS auth_sessions (
		token_hash text PRIMARY KEY,
		user_id text NOT NULL REFERENCES auth_users(id) ON DELETE CASCADE,
		expires_at timestamptz NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS auth_sessions_expiry_idx ON auth_sessions(expires_at)`,
	`CREATE TABLE IF NOT EXISTS auth_login_attempts (
		state_hash text PRIMARY KEY,
		nonce text NOT NULL,
		verifier text NOT NULL,
		expires_at timestamptz NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS auth_login_attempts_expiry_idx ON auth_login_attempts(expires_at)`,
}
