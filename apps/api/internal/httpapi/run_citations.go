package httpapi

import (
	"encoding/json"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/rag"
)

func (h *Handler) resolveRunCitations(runID, answer string) ([]domain.RAGCitation, []domain.RAGCitation, []string, error) {
	sources, err := h.citationSourcesForRun(runID)
	if err != nil {
		return nil, nil, nil, err
	}
	citations, invalidSourceIDs := rag.ResolveCitations(answer, sources)
	return sources, citations, invalidSourceIDs, nil
}

func (h *Handler) citationSourcesForRun(runID string) ([]domain.RAGCitation, error) {
	events, err := h.store.ListRunEvents(runID)
	if err != nil {
		return nil, err
	}
	catalog := map[string]domain.RAGCitation{}
	selectedSourceIDs := []string(nil)
	for _, item := range events {
		switch item.Type {
		case domain.EventRetrievalCompleted:
			var sources []domain.RAGCitation
			if decodePayloadField(item.Payload, "citation_sources", &sources) {
				catalog = make(map[string]domain.RAGCitation, len(sources))
				for _, source := range sources {
					catalog[source.SourceID] = source
				}
			}
		case domain.EventContextAssembled:
			var manifest domain.ContextManifest
			if !decodePayloadField(item.Payload, "manifest", &manifest) {
				continue
			}
			selectedSourceIDs = selectedCitationSourceIDs(manifest)
		}
	}
	sources := make([]domain.RAGCitation, 0, len(selectedSourceIDs))
	for _, sourceID := range selectedSourceIDs {
		if source, ok := catalog[sourceID]; ok {
			sources = append(sources, source)
		}
	}
	return sources, nil
}

func citationSourceIDs(citations []domain.RAGCitation) []string {
	ids := make([]string, 0, len(citations))
	for _, citation := range citations {
		ids = append(ids, citation.SourceID)
	}
	return ids
}

func selectedCitationSourceIDs(manifest domain.ContextManifest) []string {
	ids := make([]string, 0)
	seen := map[string]bool{}
	for _, entry := range manifest.Entries {
		if !entry.Selected || entry.CitationSourceID == "" || seen[entry.CitationSourceID] {
			continue
		}
		ids = append(ids, entry.CitationSourceID)
		seen[entry.CitationSourceID] = true
	}
	return ids
}

func decodePayloadField(payload map[string]any, key string, target any) bool {
	value, ok := payload[key]
	if !ok {
		return false
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return false
	}
	return json.Unmarshal(encoded, target) == nil
}
