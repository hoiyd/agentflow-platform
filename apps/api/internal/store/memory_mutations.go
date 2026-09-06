package store

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/failure"
	"agentflow-platform/apps/api/internal/redaction"
)

var (
	ErrMemoryConflict        = failure.New(failure.Definition{Message: "memory changed; refresh before retrying", Info: failure.Info{Code: "memory_version_conflict", Source: "memory", Category: failure.CategoryValidation}})
	ErrMemoryMissing         = failure.New(failure.Definition{Message: "memory not found", Info: failure.Info{Code: "memory_not_found", Source: "memory", Category: failure.CategoryValidation}})
	ErrMemoryMutationInvalid = failure.New(failure.Definition{Message: "invalid memory mutation", Info: failure.Info{Code: "memory_mutation_invalid", Source: "memory", Category: failure.CategoryValidation}})
)

func NormalizeMemoryMutation(command domain.MemoryMutation) (domain.MemoryMutation, error) {
	command.OperationID = strings.TrimSpace(command.OperationID)
	command.Content = strings.TrimSpace(command.Content)
	command.Actor = strings.TrimSpace(command.Actor)
	command.Reason = strings.TrimSpace(command.Reason)
	if command.OperationID == "" || len(command.OperationID) > 128 || command.ExpectedVersion < 1 || command.Actor == "" || len(command.Actor) > 128 || command.Reason == "" || len(command.Reason) > 512 {
		return command, ErrMemoryMutationInvalid
	}
	if _, count := redaction.Text(command.OperationID); count > 0 {
		return command, ErrMemoryMutationInvalid
	}
	if command.Action != "replace" && command.Action != "delete" {
		return command, ErrMemoryMutationInvalid
	}
	if (command.Action == "replace" && (command.Content == "" || len(command.Content) > 8000)) || (command.Action == "delete" && command.Content != "") {
		return command, ErrMemoryMutationInvalid
	}
	return command, nil
}

func memoryCommandHash(memoryID string, command domain.MemoryMutation) string {
	encoded, _ := json.Marshal(command)
	return fmt.Sprintf("sha256:%x", sha256.Sum256(append([]byte(memoryID+"\x00"), encoded...)))
}

func prepareMemoryMutation(current domain.Memory, command domain.MemoryMutation) (domain.MemoryMutationResult, error) {
	current.Version = max(1, current.Version)
	if current.Version != command.ExpectedVersion || current.DeletedAt != nil {
		return domain.MemoryMutationResult{}, ErrMemoryConflict
	}
	now := time.Now().UTC()
	actor, _ := redaction.Text(command.Actor)
	reason, _ := redaction.Text(command.Reason)
	change := domain.MemoryChange{WorkspaceID: current.WorkspaceID, MemoryID: current.ID, OperationID: command.OperationID,
		CommandHash: memoryCommandHash(current.ID, command), Action: command.Action, PreviousVersion: current.Version, Version: current.Version + 1,
		SourceMessageID: current.SourceMessageID, Actor: actor, Reason: reason, CreatedAt: now}
	current.Version++
	current.UpdatedAt = now
	current.Content = command.Content
	if command.Action == "delete" {
		current.DeletedAt = &now
		current.Metadata = map[string]any{}
	}
	return domain.MemoryMutationResult{Memory: current, Change: change, Applied: true}, nil
}

func sameMemoryCreate(existing, incoming domain.Memory) bool {
	oldMetadata, oldErr := json.Marshal(existing.Metadata)
	newMetadata, newErr := json.Marshal(incoming.Metadata)
	return oldErr == nil && newErr == nil && string(oldMetadata) == string(newMetadata) && normalizeWorkspaceID(existing.WorkspaceID) == incoming.WorkspaceID && existing.DeletedAt == nil && max(1, existing.Version) == 1 && existing.Content == incoming.Content && existing.Kind == incoming.Kind && existing.SourceMessageID == incoming.SourceMessageID && existing.UserID == incoming.UserID && existing.ProjectID == incoming.ProjectID && existing.ConversationID == incoming.ConversationID && existing.RunID == incoming.RunID
}

func suppressMemoryCandidate(candidate domain.MemoryCandidate) domain.MemoryCandidate {
	candidate.Content = "[withdrawn by memory mutation]"
	candidate.Status = domain.MemoryCandidateRejected
	candidate.PolicyReason = "source_memory_mutated"
	return candidate
}
