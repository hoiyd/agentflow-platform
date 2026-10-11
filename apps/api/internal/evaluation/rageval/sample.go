package rageval

import (
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/failure"
	"agentflow-platform/apps/api/internal/knowledge"
	"agentflow-platform/apps/api/internal/rag"
	"agentflow-platform/apps/api/internal/redaction"
	"context"
	"math"
	"time"
)

func runSample(ctx context.Context, base *knowledge.KnowledgeBase, evaluationCase domain.RAGEvaluationCase, topK int, minSimilarity float64, pipeline *PipelineIdentity) Sample {
	classification := "gating"
	if contains(evaluationCase.Tags, "non-blocking") {
		classification = "diagnostic"
	}
	sample := Sample{CaseID: evaluationCase.ID, Split: evaluationSplit(evaluationCase.Tags), Classification: classification, Status: "completed",
		Answerable: evaluationCase.Answerable != nil && *evaluationCase.Answerable, Sources: []RankedSource{}}
	if err := ctx.Err(); err != nil {
		sample.Status, sample.ErrorCode, sample.Error = "not_evaluated", failure.Describe(err).Code, err.Error()
		return sample
	}
	startedAt := time.Now()
	response, err := base.Search(ctx, domain.DocumentSearch{Query: evaluationCase.Query, Limit: topK, MinSimilarity: minSimilarity}, topK)
	sample.LatencyMS = time.Since(startedAt).Milliseconds()
	if err != nil {
		info := failure.Describe(err)
		sample.Status, sample.ErrorCode = "failed", info.Code
		sample.Error, _ = redaction.Text(err.Error())
		return sample
	}
	if pipeline.Embedding.Provider == "" {
		*pipeline = PipelineIdentity{Embedding: response.Embedding, Fusion: response.Fusion, Reranker: response.Reranker,
			RelevanceGate: response.RelevanceGate, SecurityPolicy: response.Security.PolicyVersion}
	}
	result := rag.EvaluateCase(evaluationCase, response.Items)
	sample.Passed, sample.HitAt1, sample.HitAt3, sample.HitAt5, sample.BestRank = result.Hit, result.HitAt1, result.HitAt3, result.HitAt5, result.BestRank
	sample.PredictedNoAnswer = len(response.Items) == 0
	sample.LeakCount = rag.CountForbiddenMatches(evaluationCase, response.Items)
	sample.BlockedCandidates = response.Security.BlockedCandidates
	sample.FailureReason = result.FailureReason
	if sample.Answerable && sample.BestRank > 0 {
		sample.ReciprocalRank = 1 / float64(sample.BestRank)
		sample.NDCG = 1 / math.Log2(float64(sample.BestRank)+1)
	}
	for index, item := range response.Items {
		sample.Sources = append(sample.Sources, RankedSource{Rank: index + 1, SourceURI: item.Document.SourceURI,
			DocumentVersion: item.Document.Version, ChunkHash: item.Chunk.ContentHash, Confidence: item.Confidence,
			Similarity: item.Similarity, EvidenceCoverage: item.EvidenceCoverage, FilterReason: item.FilterReason})
	}
	return sample
}
