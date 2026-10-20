package rag

import (
	"fmt"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/rag/eligibility"
	"agentflow-platform/apps/api/internal/rag/metadatafilter"
	"agentflow-platform/apps/api/internal/rag/metadataschema"
)

// EvaluateSearchResult checks the final expanded Context too: a correct Top-K
// alone cannot prove that parent/adjacent expansion preserved the restrictions.
func EvaluateSearchResult(c domain.RAGEvaluationCase, search domain.DocumentSearch, response domain.DocumentSearchResponse, started time.Time) domain.RAGEvaluationCaseResult {
	result := EvaluateCase(c, response.Items)
	result.Security = response.Security
	e := EvaluationEvidence(c, search, started)
	e.MatchedSources = maximumDistinctSourceMatches(c.ExpectedSources, response.Items)
	e.ContextTokens = response.ContextSelection.TokensUsed
	e.ContextSelection = response.ContextSelection
	e.LatencyMS = time.Since(started).Milliseconds()
	predicate, err := eligibility.CompileAt(search.MetadataFilter, started)
	var contextChunks []domain.RetrievedDocumentChunk
	for _, item := range response.ContextItems {
		sourceIDs := contextSourceChunkIDs(item)
		forbidden := false
		for _, id := range sourceIDs {
			source := item
			source.Chunk.ID = id
			contextChunks = append(contextChunks, source)
			forbidden = forbidden || goldenSourceMatchesAny(c.ForbiddenSources, source)
		}
		if err != nil || domain.NormalizeWorkspaceID(item.Document.WorkspaceID) != e.WorkspaceID ||
			!predicate.Matches(item.Document, item.Chunk) || !metadatafilter.Matches(item.Document.Metadata, item.Chunk.Metadata, search.Metadata) || forbidden {
			e.ContextLeakCount++
		}
		e.ContextSources = append(e.ContextSources, domain.RAGEvaluationSourceRevision{DocumentID: item.Document.ID,
			SourceURI: item.Document.SourceURI, DocumentVersion: item.Document.Version, UpdatedAt: item.Document.UpdatedAt,
			MetadataSchemaVersion: item.Document.MetadataSchemaVersion, ChunkHash: sourceHash(item.Chunk.Content), SourceChunkIDs: sourceIDs})
	}
	e.ContextMatchedSources = maximumDistinctSourceMatches(c.ExpectedSources, contextChunks)
	if e.ContextLeakCount > 0 {
		result.Hit = false
		result.FailureReason = fmt.Sprintf("selected Context contains %d out-of-scope source(s)", e.ContextLeakCount)
	}
	result.Evidence = e
	return result
}

// Retain restrictions and expected-source denominators even when a query fails
// or indexing prevents it from running. Missing measurements stay zero.
func EvaluationEvidence(c domain.RAGEvaluationCase, search domain.DocumentSearch, started time.Time) *domain.RAGEvaluationEvidence {
	return &domain.RAGEvaluationEvidence{Metadata: search.Metadata, MetadataFilter: search.MetadataFilter,
		SchemaVersion: metadataschema.Version, ScopePolicy: eligibility.ScopePolicyVersion,
		LifecyclePolicy: eligibility.LifecyclePolicyVersion, WorkspaceID: domain.NormalizeWorkspaceID(search.WorkspaceID),
		ObservedAt: started.UTC(), ExpectedSources: len(c.ExpectedSources),
		ContextSources: []domain.RAGEvaluationSourceRevision{}}
}
