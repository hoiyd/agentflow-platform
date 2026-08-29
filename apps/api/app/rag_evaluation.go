package app

import (
	"context"
	"fmt"

	"agentflow-platform/apps/api/internal/config"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/knowledge"
	"agentflow-platform/apps/api/internal/rag"
	"agentflow-platform/apps/api/internal/store"
)

// OfflineRAGEvaluator composes the production retrieval pipeline without
// starting the HTTP server or Agent runtime. It is intended for operator and
// CI commands, not the online product surface.
type OfflineRAGEvaluator struct {
	store     store.Store
	knowledge *knowledge.KnowledgeBase
}

func NewOfflineRAGEvaluator(cfg config.Config) (*OfflineRAGEvaluator, error) {
	backend, err := newStore(cfg)
	if err != nil {
		return nil, fmt.Errorf("create evaluation store: %w", err)
	}
	modelClient := newModelClient(cfg)
	retrievalPipeline := rag.NewRetrievalPipeline(backend)
	return &OfflineRAGEvaluator{
		store:     backend,
		knowledge: knowledge.NewKnowledgeBaseWithRetriever(backend, modelClient, retrievalPipeline),
	}, nil
}

func (e *OfflineRAGEvaluator) ListDocuments(workspaceID string) ([]domain.Document, error) {
	return e.store.ListDocumentsByWorkspace(domain.NormalizeWorkspaceID(workspaceID))
}

func (e *OfflineRAGEvaluator) Ingest(ctx context.Context, request domain.DocumentIngestRequest) (domain.Document, error) {
	request.WorkspaceID = domain.NormalizeWorkspaceID(request.WorkspaceID)
	return e.knowledge.Ingest(ctx, request)
}

func (e *OfflineRAGEvaluator) Evaluate(ctx context.Context, request domain.RAGEvaluationRunRequest) (domain.RAGEvaluationRunResponse, error) {
	request.WorkspaceID = domain.NormalizeWorkspaceID(request.WorkspaceID)
	return e.knowledge.Evaluate(ctx, request)
}

func (e *OfflineRAGEvaluator) Close() error {
	if e == nil {
		return nil
	}
	return closeStore(e.store)
}
