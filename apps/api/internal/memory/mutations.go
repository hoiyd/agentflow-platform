package memory

import (
	"context"
	"errors"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/modelprovider"
	"agentflow-platform/apps/api/internal/store"
)

// Administration is explicit user maintenance, never an autonomous ADD proposal.
type Administration interface {
	GetMemory(context.Context, string, string) (domain.MemoryDetail, error)
	MutateMemory(context.Context, string, string, domain.MemoryMutation) (domain.MemoryMutationResult, error)
}

func (p *BuiltinProvider) administration(ctx context.Context) (store.MemoryMutationStore, error) {
	if err := p.requireRunning(false); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	target, ok := p.store.(store.MemoryMutationStore)
	if !ok {
		return nil, errors.New("memory store does not support mutations")
	}
	return target, nil
}

func (p *BuiltinProvider) GetMemory(ctx context.Context, workspaceID, id string) (domain.MemoryDetail, error) {
	target, err := p.administration(ctx)
	if err != nil {
		return domain.MemoryDetail{}, err
	}
	return target.GetMemoryDetail(workspaceID, id)
}

func (p *BuiltinProvider) MutateMemory(ctx context.Context, workspaceID, id string, command domain.MemoryMutation) (domain.MemoryMutationResult, error) {
	target, err := p.administration(ctx)
	if err != nil {
		return domain.MemoryMutationResult{}, err
	}
	command, err = store.NormalizeMemoryMutation(command)
	if err != nil {
		return domain.MemoryMutationResult{}, err
	}
	previous, err := target.FindMemoryChange(workspaceID, command.OperationID)
	if err != nil {
		return domain.MemoryMutationResult{}, err
	}
	// A retry of a committed command must work even if embedding is now offline.
	// The store still validates command identity and workspace atomically.
	if previous != nil || command.Action == "delete" {
		return target.MutateMemory(workspaceID, id, command, domain.MemoryEmbedding{})
	}
	detail, err := target.GetMemoryDetail(workspaceID, id)
	if err != nil {
		return domain.MemoryMutationResult{}, err
	}
	if detail.Memory.Version != command.ExpectedVersion || detail.Memory.DeletedAt != nil {
		return domain.MemoryMutationResult{}, store.ErrMemoryConflict
	}
	var embedding modelprovider.Embedding
	if err := p.retry(ctx, "mutate.embed", func() error {
		var err error
		embedding, err = p.embedder.EmbedText(ctx, command.Content)
		return err
	}); err != nil {
		return domain.MemoryMutationResult{}, EmbeddingError{Err: err}
	}
	if err := ctx.Err(); err != nil {
		return domain.MemoryMutationResult{}, err
	}
	// Recheck the expected version in the transaction; embedding happens outside
	// the write lock and a concurrent correction may have won in the meantime.
	return target.MutateMemory(workspaceID, id, command, domain.MemoryEmbedding{
		Provider: embedding.Provider, Model: embedding.Model, Dimensions: len(embedding.Vector), Embedding: embedding.Vector,
	})
}
