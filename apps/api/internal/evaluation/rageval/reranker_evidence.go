package rageval

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"

	"agentflow-platform/apps/api/internal/rag"
)

// Evaluation queries run sequentially. Capture the exact ordered reranker input
// without retaining text or random fixture-store IDs in the report.
type rerankerEvidence struct {
	rag.Reranker
	hash      string
	latencyMS int64
}

func (r *rerankerEvidence) Rerank(ctx context.Context, request rag.RerankRequest) (rag.RerankResult, error) {
	type candidate struct {
		Workspace, Source, Version, ContentHash        string
		ChunkIndex, Start, End, Dense, Lexical, Fusion int
		Similarity, LexicalScore, RRF                  float64
	}
	items := make([]candidate, 0, len(request.Candidates))
	for _, item := range request.Candidates {
		items = append(items, candidate{item.Document.WorkspaceID, item.Document.SourceURI, item.Document.Version,
			fmt.Sprintf("%x", sha256.Sum256([]byte(item.Chunk.Content))), item.Chunk.ChunkIndex, item.Chunk.StartOffset,
			item.Chunk.EndOffset, item.VectorRank, item.LexicalRank, item.FusionRank, item.Similarity, item.LexicalScore, item.RRFScore})
	}
	encoded, err := json.Marshal(struct {
		Query      string
		Limit      int
		Candidates []candidate
	}{request.Query, request.Limit, items})
	r.hash = ""
	if err == nil {
		r.hash = fmt.Sprintf("%x", sha256.Sum256(encoded))
	}
	started := time.Now()
	result, err := r.Reranker.Rerank(ctx, request)
	r.latencyMS = time.Since(started).Milliseconds()
	return result, err
}

func sameCandidateInputs(current, baseline []Sample) bool {
	if len(current) == 0 || len(current) != len(baseline) {
		return false
	}
	byID := make(map[string]string, len(baseline))
	for _, sample := range baseline {
		if sample.CandidateSetHash == "" || byID[sample.CaseID] != "" {
			return false
		}
		byID[sample.CaseID] = sample.CandidateSetHash
	}
	for _, sample := range current {
		if sample.CandidateSetHash == "" || byID[sample.CaseID] != sample.CandidateSetHash {
			return false
		}
		delete(byID, sample.CaseID)
	}
	return len(byID) == 0
}
