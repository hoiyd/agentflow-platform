package rag

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"

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

func validateRerankResult(request RerankRequest, result RerankResult) error {
	if err := validateRerankerInfo(result.Info); err != nil {
		return err
	}
	if request.Limit > 0 && len(result.Items) > request.Limit {
		return fmt.Errorf("returned %d items for limit %d", len(result.Items), request.Limit)
	}

	candidates := make(map[string]domain.RetrievedDocumentChunk, len(request.Candidates))
	for _, candidate := range request.Candidates {
		if candidate.Chunk.ID != "" {
			candidates[candidate.Chunk.ID] = candidate
		}
	}
	seen := make(map[string]struct{}, len(result.Items))
	for index, item := range result.Items {
		if strings.TrimSpace(item.Document.ID) == "" || strings.TrimSpace(item.Chunk.ID) == "" {
			return fmt.Errorf("item %d is missing document_id or chunk_id", index)
		}
		candidate, ok := candidates[item.Chunk.ID]
		if !ok || candidate.Document.ID != item.Document.ID {
			return fmt.Errorf("item %d references unknown candidate %q", index, item.Chunk.ID)
		}
		if _, duplicate := seen[item.Chunk.ID]; duplicate {
			return fmt.Errorf("item %d duplicates chunk %q", index, item.Chunk.ID)
		}
		seen[item.Chunk.ID] = struct{}{}
		if item.RerankRank != index+1 {
			return fmt.Errorf("item %d has rerank_rank %d; expected %d", index, item.RerankRank, index+1)
		}
		if math.IsNaN(item.RerankScore) || math.IsInf(item.RerankScore, 0) {
			return fmt.Errorf("item %d has a non-finite rerank_score", index)
		}
		if result.Info.Algorithm != heuristicRerankerAlgorithm && (item.RerankScore < 0 || item.RerankScore > 1) {
			return fmt.Errorf("item %d has rerank_score outside the normalized [0,1] range", index)
		}
		if index > 0 && result.Items[index-1].RerankScore < item.RerankScore {
			return fmt.Errorf("item %d is out of descending rerank_score order", index)
		}
	}
	return nil
}

func validateRerankerInfo(info domain.RerankerInfo) error {
	if strings.TrimSpace(info.Algorithm) == "" || strings.TrimSpace(info.Version) == "" || strings.TrimSpace(info.ConfigVersion) == "" {
		return errors.New("reranker info requires algorithm, version, and config_version")
	}
	if (strings.TrimSpace(info.Provider) == "") != (strings.TrimSpace(info.Model) == "") {
		return errors.New("reranker provider and model must be supplied together")
	}
	return nil
}
