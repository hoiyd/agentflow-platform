package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"log"
	"os"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/store"

	_ "github.com/jackc/pgx/v5/stdlib"
)

type fileData struct {
	Conversations      []domain.Conversation      `json:"conversations"`
	Messages           []domain.Message           `json:"messages"`
	Agents             []domain.Agent             `json:"agents"`
	Runs               []domain.Run               `json:"runs"`
	CollaborationSteps []domain.CollaborationStep `json:"collaboration_steps"`
	TraceEvents        []domain.TraceEvent        `json:"trace_events"`
}

func main() {
	filePath := flag.String("file", ".data/agentflow.json", "file store JSON path")
	databaseURL := flag.String("database-url", os.Getenv("DATABASE_URL"), "Postgres connection URL")
	flag.Parse()

	if strings.TrimSpace(*databaseURL) == "" {
		log.Fatal("database-url or DATABASE_URL is required")
	}

	data, err := readFileData(*filePath)
	if err != nil {
		log.Fatalf("read file store: %v", err)
	}

	pgStore, err := store.NewPostgresStore(*databaseURL)
	if err != nil {
		log.Fatalf("prepare postgres store: %v", err)
	}
	if err := pgStore.Close(); err != nil {
		log.Printf("close migration bootstrap store: %v", err)
	}

	db, err := sql.Open("pgx", *databaseURL)
	if err != nil {
		log.Fatalf("open postgres: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := migrate(ctx, db, data); err != nil {
		log.Fatalf("migrate file store: %v", err)
	}
	log.Printf("migrated conversations=%d messages=%d agents=%d runs=%d collaboration_steps=%d trace_events=%d",
		len(data.Conversations), len(data.Messages), len(data.Agents), len(data.Runs), len(data.CollaborationSteps), len(data.TraceEvents))
}

func readFileData(path string) (fileData, error) {
	bytes, err := os.ReadFile(path)
	if err != nil {
		return fileData{}, err
	}
	var data fileData
	if err := json.Unmarshal(bytes, &data); err != nil {
		return fileData{}, err
	}
	return data, nil
}

func migrate(ctx context.Context, db *sql.DB, data fileData) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, item := range data.Conversations {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO conversations (id, title, created_at, updated_at)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (id) DO UPDATE SET
				title = EXCLUDED.title,
				created_at = EXCLUDED.created_at,
				updated_at = EXCLUDED.updated_at`,
			item.ID, item.Title, item.CreatedAt, item.UpdatedAt); err != nil {
			return err
		}
	}

	for _, item := range data.Agents {
		item = domain.NormalizeAgentConfig(item)
		toolsJSON, err := json.Marshal(item.Tools)
		if err != nil {
			return err
		}
		deletedAt := sql.NullTime{}
		if item.Archived {
			deletedAt = sql.NullTime{Time: item.UpdatedAt, Valid: true}
			if deletedAt.Time.IsZero() {
				deletedAt.Time = time.Now().UTC()
			}
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO agents (id, name, description, system_prompt, tools, memory_enabled, retrieval_enabled, executor, deleted_at, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
			ON CONFLICT (id) DO UPDATE SET
				name = EXCLUDED.name,
				description = EXCLUDED.description,
				system_prompt = EXCLUDED.system_prompt,
				tools = EXCLUDED.tools,
				memory_enabled = EXCLUDED.memory_enabled,
				retrieval_enabled = EXCLUDED.retrieval_enabled,
				executor = EXCLUDED.executor,
				deleted_at = EXCLUDED.deleted_at,
				created_at = EXCLUDED.created_at,
				updated_at = EXCLUDED.updated_at`,
			item.ID, item.Name, item.Description, item.SystemPrompt, toolsJSON, item.MemoryEnabled, item.RetrievalEnabled, item.Executor, deletedAt, item.CreatedAt, item.UpdatedAt); err != nil {
			return err
		}
	}

	for _, item := range data.Messages {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO messages (id, conversation_id, role, content, created_at)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (id) DO UPDATE SET
				conversation_id = EXCLUDED.conversation_id,
				role = EXCLUDED.role,
				content = EXCLUDED.content,
				created_at = EXCLUDED.created_at`,
			item.ID, item.ConversationID, item.Role, item.Content, item.CreatedAt); err != nil {
			return err
		}
	}

	for _, item := range data.Runs {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO runs (id, agent_id, conversation_id, status, error, runtime, workflow_id, workflow_run_id, workflow_status, started_at, completed_at, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
			ON CONFLICT (id) DO UPDATE SET
				agent_id = EXCLUDED.agent_id,
				conversation_id = EXCLUDED.conversation_id,
				status = EXCLUDED.status,
				error = EXCLUDED.error,
				runtime = EXCLUDED.runtime,
				workflow_id = EXCLUDED.workflow_id,
				workflow_run_id = EXCLUDED.workflow_run_id,
				workflow_status = EXCLUDED.workflow_status,
				started_at = EXCLUDED.started_at,
				completed_at = EXCLUDED.completed_at,
				created_at = EXCLUDED.created_at,
				updated_at = EXCLUDED.updated_at`,
			item.ID, item.AgentID, item.ConversationID, string(item.Status), item.Error, item.Runtime, item.WorkflowID, item.WorkflowRunID, item.WorkflowStatus, item.StartedAt, item.CompletedAt, item.CreatedAt, item.UpdatedAt); err != nil {
			return err
		}
	}

	for _, item := range data.CollaborationSteps {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO collaboration_steps (id, run_id, conversation_id, role, agent_id, status, iteration, input, output, error, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
			ON CONFLICT (id) DO UPDATE SET
				run_id = EXCLUDED.run_id,
				conversation_id = EXCLUDED.conversation_id,
				role = EXCLUDED.role,
				agent_id = EXCLUDED.agent_id,
				status = EXCLUDED.status,
				iteration = EXCLUDED.iteration,
				input = EXCLUDED.input,
				output = EXCLUDED.output,
				error = EXCLUDED.error,
				created_at = EXCLUDED.created_at,
				updated_at = EXCLUDED.updated_at`,
			item.ID, item.RunID, item.ConversationID, item.Role, item.AgentID, string(item.Status), item.Iteration, item.Input, item.Output, item.Error, item.CreatedAt, item.UpdatedAt); err != nil {
			return err
		}
	}

	for _, item := range data.TraceEvents {
		payloadJSON, err := json.Marshal(item.Payload)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO trace_events (id, run_id, step_id, type, payload, timestamp, duration_ms)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT (id) DO UPDATE SET
				run_id = EXCLUDED.run_id,
				step_id = EXCLUDED.step_id,
				type = EXCLUDED.type,
				payload = EXCLUDED.payload,
				timestamp = EXCLUDED.timestamp,
				duration_ms = EXCLUDED.duration_ms`,
			item.ID, item.RunID, nullString(item.StepID), string(item.Type), payloadJSON, item.Timestamp, item.DurationMS); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		if errors.Is(err, context.Canceled) {
			return context.Canceled
		}
		return err
	}
	return nil
}

func nullString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}
