package rag

import (
	"agentflow-platform/apps/api/internal/domain"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// AssignRunCitationSources keeps one namespace for automatic and Tool evidence.
// HTTP search without a Run still uses AssignCitationSources.
func AssignRunCitationSources(items []domain.RetrievedDocumentChunk, existing []domain.RAGCitation) ([]domain.RetrievedDocumentChunk, []domain.RAGCitation) {
	assigned, sources := AssignCitationSources(items)
	identities := map[string]string{}
	next := 0
	for _, source := range existing {
		identities[citationIdentity(source)] = source.SourceID
		number, _ := strconv.Atoi(strings.TrimPrefix(source.SourceID, "S"))
		next = max(next, number)
	}
	for index, source := range sources {
		id := identities[citationIdentity(source)]
		if id == "" {
			next++
			id = fmt.Sprintf("S%d", next)
			identities[citationIdentity(source)] = id
		}
		sources[index].SourceID = id
		assigned[index].SourceID = id
	}
	return assigned, sources
}

func citationIdentity(source domain.RAGCitation) string {
	return source.DocumentID + "\x00" + source.DocumentVersion + "\x00" + source.ChunkID
}

// RunCitationReservations reserves IDs for search locators without making them
// evidence. A batch finishes tracing after its handlers run, so numbering from
// read completions alone would assign the same ID to two newly read chunks.
func RunCitationReservations(events []domain.RunEvent) []domain.RAGCitation {
	sources := []domain.RAGCitation{}
	positions := map[string]int{}
	for _, event := range events {
		search := event.Type == domain.EventToolCompleted && event.Payload["tool_name"] == domain.KnowledgeSearchToolName
		candidates := CitationSourcesFromEvents([]domain.RunEvent{event})
		if search {
			if event.Payload["truncated"] == true || event.Payload["error"] != "" && event.Payload["error"] != nil {
				continue
			}
			var output domain.KnowledgeToolSearchResult
			data, _ := json.Marshal(event.Payload["result"])
			if json.Unmarshal(data, &output) != nil {
				continue
			}
			items := []domain.RetrievedDocumentChunk{}
			for _, item := range output.Items {
				if item.Reference != "" && item.DocumentID != "" {
					items = append(items, domain.RetrievedDocumentChunk{Document: domain.Document{ID: item.DocumentID, Version: item.DocumentVersion}, Chunk: domain.DocumentChunk{ID: item.Reference}})
				}
			}
			_, candidates = AssignRunCitationSources(items, sources)
		}
		for _, source := range candidates {
			key := citationIdentity(source)
			if index, ok := positions[key]; ok {
				if !search {
					sources[index] = source
				}
			} else {
				positions[key] = len(sources)
				sources = append(sources, source)
			}
		}
	}
	return sources
}

// CitationSourcesFromEvents reads only server-owned retrieval and completed
// Knowledge read records; model-written citation metadata is never a catalog.
func CitationSourcesFromEvents(events []domain.RunEvent) []domain.RAGCitation {
	sources := []domain.RAGCitation{}
	positions := map[string]int{}
	for _, event := range events {
		var candidates []domain.RAGCitation
		switch event.Type {
		case domain.EventRetrievalCompleted:
			data, _ := json.Marshal(event.Payload["citation_sources"])
			_ = json.Unmarshal(data, &candidates)
		case domain.EventToolCompleted:
			result, ok := KnowledgeReadFromEvent(event)
			if !ok {
				continue
			}
			candidates = []domain.RAGCitation{result.Source}
		}
		for _, source := range candidates {
			if source.SourceID == "" || source.ChunkID == "" {
				continue
			}
			if index, ok := positions[source.SourceID]; ok {
				sources[index] = source
			} else {
				positions[source.SourceID] = len(sources)
				sources = append(sources, source)
			}
		}
	}
	return sources
}

// KnowledgeReadFromEvent accepts only complete, successful server-owned records.
// Page truncation means more content exists; executor truncation means evidence
// was spilled or lost and cannot support a citation.
func KnowledgeReadFromEvent(event domain.RunEvent) (domain.KnowledgeToolReadResult, bool) {
	if event.Type != domain.EventToolCompleted || event.ID == "" || event.Payload["tool_name"] != domain.KnowledgeReadToolName || event.Payload["truncated"] == true || event.Payload["error"] != "" && event.Payload["error"] != nil {
		return domain.KnowledgeToolReadResult{}, false
	}
	data, err := json.Marshal(event.Payload["result"])
	var result domain.KnowledgeToolReadResult
	if err != nil || json.Unmarshal(data, &result) != nil || result.Content == "" || result.Source.SourceID == "" || result.Source.ChunkID == "" {
		return domain.KnowledgeToolReadResult{}, false
	}
	result.Source.RunID = event.RunID
	result.Source.ToolEventID = event.ID
	result.Source.ToolCallID, _ = event.Payload["tool_call_id"].(string)
	return result, result.Source.ToolCallID != ""
}
