package agent

import (
	"context"
	"strings"

	"agentflow-platform/apps/api/internal/domain"
	eventpkg "agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/failure"
	memorypkg "agentflow-platform/apps/api/internal/memory"
	"agentflow-platform/apps/api/internal/rag"
	"agentflow-platform/apps/api/internal/sessionhistory"
)

func (r *Runtime) retrieveSessionHistory(ctx context.Context, runID string, conversationID string, queryText string) []domain.RetrievedSessionHistory {
	snapshot, err := r.snapshotForRun(runID)
	if err != nil || !snapshot.ContextAssembly.HistoryRetrievalEnabled {
		return nil
	}
	keywords := sessionhistory.Keywords(queryText)
	if len(keywords) == 0 {
		return nil
	}
	_ = r.runEventSink().Publish(ctx, domain.RunEvent{
		Type: domain.EventHistorySearchStarted, RunID: runID, ConversationID: conversationID,
		Payload: sessionHistorySearchPayload(eventpkg.SessionHistorySearchPayload{
			Query: truncateRuntimeText(queryText, 1200), Keywords: keywords,
		}),
	})
	result, err := sessionhistory.Search(r.store, sessionhistory.Query{
		ConversationID: conversationID, Keywords: keywords,
		NeighborWindow: snapshot.ContextAssembly.HistoryRetrievalWindow,
		MaxResults:     snapshot.ContextAssembly.HistoryRetrievalMaxResults,
		MaxCharacters:  snapshot.ContextAssembly.HistoryRetrievalMaxChars,
		MaxTokens:      snapshot.ContextAssembly.HistoryRetrievalMaxTokens,
		ExcludeRunID:   runID, ExcludeLatestMessage: true,
	})
	payload := eventpkg.SessionHistorySearchPayload{
		Query: truncateRuntimeText(queryText, 1200), Keywords: keywords,
		ResultCount: len(result.Items), DirectMatchCount: result.DirectMatches, Truncated: result.Truncated,
	}
	if err != nil {
		payload.Error = err.Error()
		_ = r.runEventSink().Publish(ctx, domain.RunEvent{Type: domain.EventHistorySearchFailed, RunID: runID, ConversationID: conversationID, Payload: failure.Merge(sessionHistorySearchPayload(payload), err)})
		return nil
	}
	references := make([]string, 0, len(result.Items))
	for _, item := range result.Items {
		references = append(references, item.Reference)
	}
	payload.SourceReferences = references
	_ = r.runEventSink().Publish(ctx, domain.RunEvent{Type: domain.EventHistorySearchCompleted, RunID: runID, ConversationID: conversationID, Payload: sessionHistorySearchPayload(payload)})
	return result.Items
}

func sessionHistorySearchPayload(payload eventpkg.SessionHistorySearchPayload) map[string]any {
	encoded, _ := eventpkg.Payload(payload)
	return encoded
}

type retrievalQuerySource string

const (
	retrievalQuerySourceUserInput          retrievalQuerySource = "user_input"
	retrievalQuerySourceBoundedSubquestion retrievalQuerySource = "bounded_subquestion"
)

type retrievalQuery struct {
	Text    string
	Source  retrievalQuerySource
	StageID string
}

func userInputRetrievalQuery(text string) retrievalQuery {
	return retrievalQuery{Text: text, Source: retrievalQuerySourceUserInput}
}

func (q retrievalQuery) normalize() (retrievalQuery, string) {
	q.Text = strings.TrimSpace(q.Text)
	q.StageID = strings.TrimSpace(q.StageID)
	if q.Text == "" {
		return q, "empty_query"
	}
	switch q.Source {
	case retrievalQuerySourceUserInput, retrievalQuerySourceBoundedSubquestion:
		return q, ""
	case "":
		return q, "query_source_required"
	default:
		return q, "unsupported_query_source"
	}
}

func (r *Runtime) retrieveContext(ctx context.Context, runID string, query retrievalQuery, memoryEnabled bool, retrievalEnabled bool, metadata map[string]any) ([]domain.RetrievedMemory, []domain.RetrievedDocumentChunk) {
	conversationID := ""
	workspaceID := ""
	if run, ok, _ := r.store.GetRun(runID); ok {
		conversationID = run.ConversationID
		workspaceID = run.WorkspaceID
	}
	query, skipReason := query.normalize()
	queryPayload := map[string]any{
		"query":        truncateRuntimeText(query.Text, 1200),
		"query_source": string(query.Source),
	}
	r.publishRetrievalEvent(ctx, domain.EventRetrievalStarted, runID, conversationID, query.StageID, queryPayload)
	embeddingQuery := rag.EmbeddingQuery(query.Text)
	payload := map[string]any{}
	for key, value := range metadata {
		payload[key] = value
	}
	payload["workspace_id"] = workspaceID
	payload["query"] = truncateRuntimeText(query.Text, 1200)
	payload["query_source"] = string(query.Source)
	payload["embedding_query_chars"] = len(embeddingQuery)
	payload["embedding_query_original_chars"] = len(query.Text)
	payload["embedding_query_truncated"] = len(embeddingQuery) < len(query.Text)
	if skipReason != "" {
		payload["query_skipped"] = true
		payload["query_skip_reason"] = skipReason
		payload["memory_count"] = 0
		payload["chunk_count"] = 0
		payload["matched_chunk_count"] = 0
		r.publishRetrievalEvent(ctx, domain.EventRetrievalCompleted, runID, conversationID, query.StageID, payload)
		return nil, nil
	}
	if !memoryEnabled && !retrievalEnabled {
		payload["memory_count"] = 0
		payload["chunk_count"] = 0
		r.publishRetrievalEvent(ctx, domain.EventRetrievalCompleted, runID, conversationID, query.StageID, payload)
		return nil, nil
	}
	client, err := r.embeddingClientForRun(runID)
	if err != nil {
		payload["error"] = err.Error()
		r.publishRetrievalEvent(ctx, domain.EventRetrievalFailed, runID, conversationID, query.StageID, failure.Merge(payload, err))
		return nil, nil
	}
	embedding, err := rag.EmbedQuery(ctx, query.Text, func(ctx context.Context, query string) (rag.Embedding, error) {
		result, embedErr := client.EmbedText(ctx, query)
		return rag.Embedding{
			Vector:     result.Vector,
			Provider:   result.Provider,
			Model:      result.Model,
			Dimensions: result.Dimensions,
			Estimated:  result.Estimated,
		}, embedErr
	})
	if err != nil {
		payload["error"] = err.Error()
		r.publishRetrievalEvent(ctx, domain.EventRetrievalFailed, runID, conversationID, query.StageID, failure.Merge(payload, err))
		return nil, nil
	}
	payload["embedding_provider"] = embedding.Provider
	payload["embedding_model"] = embedding.Model
	payload["embedding_dimensions"] = embedding.Dimensions
	payload["embedding_estimated"] = embedding.Estimated
	var memories []domain.RetrievedMemory
	if memoryEnabled {
		var recallErr error
		if r.memoryRecall == nil {
			recallErr = memorypkg.ErrProviderNotInitialized
		} else {
			memories, recallErr = r.memoryRecall.Recall(ctx, domain.MemorySearch{
				WorkspaceID:       workspaceID,
				Query:             query.Text,
				Embedding:         embedding.Vector,
				EmbeddingProvider: embedding.Provider,
				EmbeddingModel:    embedding.Model,
				Limit:             5,
			})
		}
		if recallErr != nil {
			payload["memory_error"] = recallErr.Error()
			memories = nil
			memoryPayload := map[string]any{
				"workspace_id": workspaceID,
				"query":        truncateRuntimeText(query.Text, 1200),
				"query_source": string(query.Source),
				"error":        recallErr.Error(),
			}
			_ = r.runEventSink().Publish(ctx, domain.RunEvent{
				Type:           domain.EventMemoryRecallFailed,
				RunID:          runID,
				ConversationID: conversationID,
				StageID:        query.StageID,
				Payload:        failure.Merge(memoryPayload, recallErr),
			})
		}
	}
	var chunks []domain.RetrievedDocumentChunk
	var matchedChunks []domain.RetrievedDocumentChunk
	if retrievalEnabled {
		if r.knowledgeRetriever == nil {
			payload["rag_error"] = "knowledge retriever is not configured"
		} else {
			knowledgeContextMaxTokens := r.contextAssemblyConfig.KnowledgeMaxTokens
			if snapshot, snapshotErr := r.snapshotForRun(runID); snapshotErr == nil {
				knowledgeContextMaxTokens = snapshot.ContextAssembly.KnowledgeMaxTokens
			}
			response, searchErr := r.knowledgeRetriever.Search(ctx, domain.DocumentSearch{
				WorkspaceID:               workspaceID,
				Query:                     query.Text,
				Limit:                     5,
				KnowledgeContextMaxTokens: knowledgeContextMaxTokens,
			}, 5, embedding)
			if searchErr != nil {
				payload["rag_error"] = searchErr.Error()
			} else {
				matchedChunks = response.Items
				if response.ContextItems != nil {
					chunks = response.ContextItems
				} else {
					chunks = response.Items
				}
				payload["fusion"] = response.Fusion
				payload["reranker"] = response.Reranker
				payload["relevance_gate"] = response.RelevanceGate
				payload["relevance_decisions"] = response.RelevanceDecisions
				payload["citation_sources"] = response.CitationSources
				payload["knowledge_security"] = response.Security
				if response.ContextSelection.Version != "" {
					payload["context_selection"] = response.ContextSelection
				}
				payload["rag_no_match"] = response.NoMatch
				if response.Reason != "" {
					payload["rag_no_match_reason"] = response.Reason
				}
			}
		}
	}
	payload["memory_count"] = len(memories)
	payload["chunk_count"] = len(chunks)
	payload["matched_chunk_count"] = len(matchedChunks)
	for key, value := range retrievalTracePayload(memories, chunks) {
		payload[key] = value
	}
	if len(matchedChunks) > 0 {
		payload["matched_chunks"] = retrievedChunkTraceItems(matchedChunks)
	}
	r.publishRetrievalEvent(ctx, domain.EventRetrievalCompleted, runID, conversationID, query.StageID, payload)
	return memories, chunks
}

func (r *Runtime) publishRetrievalEvent(ctx context.Context, eventType domain.RunEventType, runID, conversationID, stageID string, payload map[string]any) {
	_ = r.runEventSink().Publish(ctx, domain.RunEvent{
		Type: eventType, RunID: runID, ConversationID: conversationID, StageID: stageID, Payload: payload,
	})
}

func retrievalTracePayload(memories []domain.RetrievedMemory, chunks []domain.RetrievedDocumentChunk) map[string]any {
	payload := map[string]any{}
	if len(memories) > 0 {
		items := make([]map[string]any, 0, len(memories))
		for _, memory := range memories {
			items = append(items, map[string]any{
				"id":         memory.Memory.ID,
				"kind":       memory.Memory.Kind,
				"content":    truncateRuntimeText(memory.Memory.Content, 1200),
				"metadata":   memory.Memory.Metadata,
				"similarity": memory.Similarity,
				"score":      memory.Score,
			})
		}
		payload["retrieved_memories"] = items
	}
	if len(chunks) > 0 {
		payload["retrieved_chunks"] = retrievedChunkTraceItems(chunks)
	}
	return payload
}

func retrievedChunkTraceItems(chunks []domain.RetrievedDocumentChunk) []map[string]any {
	items := make([]map[string]any, 0, len(chunks))
	for _, chunk := range chunks {
		items = append(items, map[string]any{
			"source_id":          chunk.SourceID,
			"document_id":        chunk.Document.ID,
			"document_title":     chunk.Document.Title,
			"document_version":   chunk.Chunk.DocumentVersion,
			"chunk_id":           chunk.Chunk.ID,
			"parent_id":          chunk.Chunk.ParentID,
			"section_path":       chunk.Chunk.SectionPath,
			"start_offset":       chunk.Chunk.StartOffset,
			"end_offset":         chunk.Chunk.EndOffset,
			"content_hash":       chunk.Chunk.ContentHash,
			"chunk_index":        chunk.Chunk.ChunkIndex,
			"content":            truncateRuntimeText(chunk.Chunk.Content, 1600),
			"metadata":           chunk.Chunk.Metadata,
			"similarity":         chunk.Similarity,
			"score":              chunk.Score,
			"vector_rank":        chunk.VectorRank,
			"lexical_rank":       chunk.LexicalRank,
			"lexical_score":      chunk.LexicalScore,
			"rrf_score":          chunk.RRFScore,
			"fusion_rank":        chunk.FusionRank,
			"rerank_rank":        chunk.RerankRank,
			"rerank_score":       chunk.RerankScore,
			"confidence":         chunk.Confidence,
			"filter_reason":      chunk.FilterReason,
			"context_role":       chunk.ContextRole,
			"matched_chunk_id":   chunk.MatchedChunkID,
			"source_chunk_ids":   chunk.SourceChunkIDs,
			"matched_chunk_ids":  chunk.MatchedChunkIDs,
			"merged_chunk_count": chunk.MergedChunkCount,
		})
	}
	return items
}

func truncateRuntimeText(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return value[:limit] + "...[truncated]"
}
