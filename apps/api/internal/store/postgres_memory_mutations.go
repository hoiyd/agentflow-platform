package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"agentflow-platform/apps/api/internal/domain"
)

const memoryColumns = `id,workspace_id,user_id,project_id,conversation_id,run_id,source_message_id,kind,content,metadata,created_at,updated_at,version,deleted_at`
const memoryChangeColumns = `workspace_id,memory_id,operation_id,command_hash,action,previous_version,version,source_message_id,actor,reason,created_at`

// The source fence must cover candidate creation as well as materialization.
// ponytail: serialize Memory writes; use source-scoped locks if write throughput grows.
func (s *PostgresStore) beginMemoryWrite() (*sql.Tx, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`SELECT pg_advisory_xact_lock(817017)`); err != nil {
		tx.Rollback()
		return nil, err
	}
	return tx, nil
}

func (s *PostgresStore) GetMemoryDetail(workspaceID, id string) (domain.MemoryDetail, error) {
	tx, err := s.db.BeginTx(context.Background(), &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return domain.MemoryDetail{}, err
	}
	defer tx.Rollback()
	item, err := scanMemory(tx.QueryRow(`SELECT `+memoryColumns+` FROM memories WHERE workspace_id=$1 AND id=$2`, NormalizeWorkspaceID(workspaceID), id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.MemoryDetail{}, ErrMemoryMissing
	}
	if err != nil {
		return domain.MemoryDetail{}, err
	}
	rows, err := tx.Query(`SELECT `+memoryChangeColumns+` FROM memory_changes WHERE workspace_id=$1 AND memory_id=$2 ORDER BY version DESC LIMIT 100`, item.WorkspaceID, id)
	if err != nil {
		return domain.MemoryDetail{}, err
	}
	defer rows.Close()
	detail := domain.MemoryDetail{Memory: item, Changes: []domain.MemoryChange{}}
	for rows.Next() {
		change, err := scanMemoryChange(rows)
		if err != nil {
			return domain.MemoryDetail{}, err
		}
		detail.Changes = append(detail.Changes, change)
	}
	if err := rows.Err(); err != nil {
		return domain.MemoryDetail{}, err
	}
	return detail, tx.Commit()
}

func (s *PostgresStore) FindMemoryChange(workspaceID, operationID string) (*domain.MemoryChange, error) {
	change, err := scanMemoryChange(s.db.QueryRow(`SELECT `+memoryChangeColumns+` FROM memory_changes WHERE workspace_id=$1 AND operation_id=$2`, NormalizeWorkspaceID(workspaceID), operationID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &change, nil
}

func (s *PostgresStore) MutateMemory(workspaceID, id string, command domain.MemoryMutation, embedding domain.MemoryEmbedding) (domain.MemoryMutationResult, error) {
	command, err := NormalizeMemoryMutation(command)
	if err != nil {
		return domain.MemoryMutationResult{}, err
	}
	workspaceID = NormalizeWorkspaceID(workspaceID)
	tx, err := s.beginMemoryWrite()
	if err != nil {
		return domain.MemoryMutationResult{}, err
	}
	defer tx.Rollback()
	current, err := scanMemory(tx.QueryRow(`SELECT `+memoryColumns+` FROM memories WHERE workspace_id=$1 AND id=$2 FOR UPDATE`, workspaceID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.MemoryMutationResult{}, ErrMemoryMissing
	}
	if err != nil {
		return domain.MemoryMutationResult{}, err
	}
	previous, err := scanMemoryChange(tx.QueryRow(`SELECT `+memoryChangeColumns+` FROM memory_changes WHERE workspace_id=$1 AND operation_id=$2`, workspaceID, command.OperationID))
	if err == nil {
		if previous.MemoryID != id || previous.CommandHash != MemoryCommandHash(id, command) {
			return domain.MemoryMutationResult{}, ErrMemoryConflict
		}
		return domain.MemoryMutationResult{Memory: current, Change: previous, Applied: false}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return domain.MemoryMutationResult{}, err
	}
	result, err := PrepareMemoryMutation(current, command)
	if err != nil {
		return domain.MemoryMutationResult{}, err
	}
	if command.Action == "replace" && (len(embedding.Embedding) != 1536 || embedding.Dimensions != 1536) {
		return domain.MemoryMutationResult{}, ErrMemoryMutationInvalid
	}
	metadata, err := json.Marshal(result.Memory.Metadata)
	if err != nil {
		return domain.MemoryMutationResult{}, err
	}
	if _, err := tx.Exec(`UPDATE memories SET content=$3,metadata=$4,version=$5,deleted_at=$6,updated_at=$7 WHERE workspace_id=$1 AND id=$2`, workspaceID, id, result.Memory.Content, metadata, result.Memory.Version, result.Memory.DeletedAt, result.Memory.UpdatedAt); err != nil {
		return domain.MemoryMutationResult{}, err
	}
	if _, err := tx.Exec(`DELETE FROM memory_embeddings WHERE memory_id=$1`, id); err != nil {
		return domain.MemoryMutationResult{}, err
	}
	if command.Action == "replace" {
		if _, err := tx.Exec(`INSERT INTO memory_embeddings (memory_id,provider,model,dimensions,embedding,created_at) VALUES ($1,$2,$3,$4,$5::vector,$6)`, id, embedding.Provider, embedding.Model, embedding.Dimensions, vectorLiteral(embedding.Embedding), result.Change.CreatedAt); err != nil {
			return domain.MemoryMutationResult{}, err
		}
	}
	if current.SourceMessageID != "" {
		withdrawn := SuppressMemoryCandidate(domain.MemoryCandidate{})
		if _, err := tx.Exec(`UPDATE memory_candidates SET content=$2,status=$3,policy_reason=$4 WHERE source_message_id=$1 AND workspace_id=$5`, current.SourceMessageID, withdrawn.Content, withdrawn.Status, withdrawn.PolicyReason, workspaceID); err != nil {
			return domain.MemoryMutationResult{}, err
		}
	}
	c := result.Change
	if _, err := tx.Exec(`INSERT INTO memory_changes (`+memoryChangeColumns+`) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, c.WorkspaceID, c.MemoryID, c.OperationID, c.CommandHash, c.Action, c.PreviousVersion, c.Version, c.SourceMessageID, c.Actor, c.Reason, c.CreatedAt); err != nil {
		return domain.MemoryMutationResult{}, err
	}
	return result, tx.Commit()
}

func scanMemoryChange(row scanner) (domain.MemoryChange, error) {
	var c domain.MemoryChange
	err := row.Scan(&c.WorkspaceID, &c.MemoryID, &c.OperationID, &c.CommandHash, &c.Action, &c.PreviousVersion, &c.Version, &c.SourceMessageID, &c.Actor, &c.Reason, &c.CreatedAt)
	return c, err
}
