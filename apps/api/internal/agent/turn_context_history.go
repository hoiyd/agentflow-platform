package agent

import (
	"context"

	"agentflow-platform/apps/api/internal/contextassembly"
	"agentflow-platform/apps/api/internal/domain"
	eventpkg "agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/failure"
	"agentflow-platform/apps/api/internal/sessionhistory"
)

func (s turnContextSessions) retrieveSessionHistory(ctx context.Context, runID, conversationID, queryText string) []domain.RetrievedSessionHistory {
	run, ok, err := s.store.GetRun(runID)
	if err != nil || !ok || validateRuntimeSnapshot(run.RuntimeSnapshot) != nil {
		return nil
	}
	config := contextassembly.NormalizeConfig(run.RuntimeSnapshot.ContextAssembly)
	if !config.HistoryRetrievalEnabled {
		return nil
	}
	keywords := sessionhistory.Keywords(queryText)
	if len(keywords) == 0 {
		return nil
	}
	_ = s.sink.Publish(ctx, domain.RunEvent{
		Type: domain.EventHistorySearchStarted, RunID: runID, ConversationID: conversationID,
		Payload: sessionHistorySearchPayload(eventpkg.SessionHistorySearchPayload{
			Query: truncateRuntimeText(queryText, 1200), Keywords: keywords,
		}),
	})
	result, err := sessionhistory.Search(s.store, sessionhistory.Query{
		ConversationID: conversationID, Keywords: keywords,
		NeighborWindow: config.HistoryRetrievalWindow,
		MaxResults:     config.HistoryRetrievalMaxResults,
		MaxCharacters:  config.HistoryRetrievalMaxChars,
		MaxTokens:      config.HistoryRetrievalMaxTokens,
		ExcludeRunID:   runID, ExcludeLatestMessage: true,
	})
	payload := eventpkg.SessionHistorySearchPayload{
		Query: truncateRuntimeText(queryText, 1200), Keywords: keywords,
		ResultCount: len(result.Items), DirectMatchCount: result.DirectMatches, Truncated: result.Truncated,
	}
	if err != nil {
		payload.Error = err.Error()
		_ = s.sink.Publish(ctx, domain.RunEvent{Type: domain.EventHistorySearchFailed, RunID: runID, ConversationID: conversationID, Payload: failure.Merge(sessionHistorySearchPayload(payload), err)})
		return nil
	}
	references := make([]string, 0, len(result.Items))
	for _, item := range result.Items {
		references = append(references, item.Reference)
	}
	payload.SourceReferences = references
	_ = s.sink.Publish(ctx, domain.RunEvent{Type: domain.EventHistorySearchCompleted, RunID: runID, ConversationID: conversationID, Payload: sessionHistorySearchPayload(payload)})
	return result.Items
}

func sessionHistorySearchPayload(payload eventpkg.SessionHistorySearchPayload) map[string]any {
	encoded, _ := eventpkg.Payload(payload)
	return encoded
}
