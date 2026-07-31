package rag

import (
	"context"

	"agentflow-platform/apps/api/internal/domain"
)

// RerankRequest is provider-neutral so deterministic and model-backed
// rerankers can share the same RetrievalPipeline contract.
type RerankRequest struct {
	Query      string
	Candidates []domain.RetrievedDocumentChunk
	Limit      int
}

type RerankResult struct {
	Items []domain.RetrievedDocumentChunk
	Info  domain.RerankerInfo
}

// Reranker orders fused recall candidates. Implementations must return ranking
// evidence and the identity/configuration actually used for this request.
type Reranker interface {
	Rerank(context.Context, RerankRequest) (RerankResult, error)
}
