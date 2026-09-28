package rag

import (
	"agentflow-platform/apps/api/internal/domain"
	"encoding/json"
	"testing"
)

func TestRunCitationSourcesKeepAutomaticAndToolEvidenceDistinct(t *testing.T) {
	original := domain.RAGCitation{SourceID: "S1", DocumentID: "auto", ChunkID: "chunk-auto"}
	events := []domain.RunEvent{{Type: domain.EventRetrievalCompleted, Payload: map[string]any{"citation_sources": []domain.RAGCitation{original}}}}
	items, sources := AssignRunCitationSources([]domain.RetrievedDocumentChunk{{Document: domain.Document{ID: "tool-doc"}, Chunk: domain.DocumentChunk{ID: "tool-chunk"}}}, CitationSourcesFromEvents(events))
	if items[0].SourceID != "S2" || sources[0].SourceID != "S2" {
		t.Fatalf("source collision: %#v", sources)
	}
	events = append(events, domain.RunEvent{ID: "tool-event", Type: domain.EventToolCompleted, Payload: map[string]any{"tool_name": "knowledge_read", "tool_call_id": "call-read", "result": domain.KnowledgeToolReadResult{Source: sources[0], Content: "evidence"}}})
	catalog := CitationSourcesFromEvents(events)
	if len(catalog) != 2 || catalog[1].ToolCallID != "call-read" || catalog[1].ToolEventID != "tool-event" {
		t.Fatalf("missing provenance: %#v", catalog)
	}
	_, again := AssignRunCitationSources(items, catalog)
	if again[0].SourceID != "S2" {
		t.Fatal("same chunk identity renumbered")
	}
	events[len(events)-1].Payload["truncated"] = true
	if len(CitationSourcesFromEvents(events)) != 1 {
		t.Fatal("spilled read became evidence")
	}
}

func TestRunCitationReservationsAreNotEvidenceAndRejectMalformedEvents(t *testing.T) {
	search := domain.RunEvent{ID: "search-event", Type: domain.EventToolCompleted, Payload: map[string]any{"tool_name": "knowledge_search", "result": domain.KnowledgeToolSearchResult{Items: []domain.KnowledgeMatch{{Reference: "chunk-1", DocumentID: "doc-1"}}}}}
	events := []domain.RunEvent{search, search}
	reserved := RunCitationReservations(events)
	if len(reserved) != 1 || reserved[0].SourceID != "S1" || len(CitationSourcesFromEvents(events)) != 0 {
		t.Fatalf("reserved=%#v", reserved)
	}
	for _, result := range []any{json.RawMessage(`"not an object"`), domain.KnowledgeToolSearchResult{Items: []domain.KnowledgeMatch{{}}}} {
		search.Payload["result"] = result
		if len(RunCitationReservations([]domain.RunEvent{search})) != 0 {
			t.Fatal("malformed search created a reservation")
		}
	}
	for _, event := range []domain.RunEvent{
		{Type: domain.EventToolFailed},
		{ID: "bad-result", Type: domain.EventToolCompleted, Payload: map[string]any{"tool_name": "knowledge_read", "result": json.RawMessage(`"not an object"`)}},
		{ID: "missing-call", Type: domain.EventToolCompleted, Payload: map[string]any{"tool_name": "knowledge_read", "result": domain.KnowledgeToolReadResult{Content: "text", Source: domain.RAGCitation{SourceID: "S1", ChunkID: "chunk-1"}}}},
		{ID: "bad-source", Type: domain.EventRetrievalCompleted, Payload: map[string]any{"citation_sources": []domain.RAGCitation{{SourceID: "S1"}}}},
	} {
		if len(CitationSourcesFromEvents([]domain.RunEvent{event})) != 0 {
			t.Fatalf("bad event became source: %#v", event)
		}
	}
	original := domain.RAGCitation{SourceID: "S1", DocumentID: "doc", ChunkID: "chunk-1"}
	updated := original
	updated.DocumentTitle = "latest"
	sources := CitationSourcesFromEvents([]domain.RunEvent{{Type: domain.EventRetrievalCompleted, Payload: map[string]any{"citation_sources": []domain.RAGCitation{original}}}, {Type: domain.EventRetrievalCompleted, Payload: map[string]any{"citation_sources": []domain.RAGCitation{updated}}}})
	if len(sources) != 1 || sources[0].DocumentTitle != "latest" {
		t.Fatalf("sources=%#v", sources)
	}
}

func TestRunCitationReservationsStayStableAcrossLaterRetrieval(t *testing.T) {
	events := []domain.RunEvent{{ID: "search", Type: domain.EventToolCompleted, Payload: map[string]any{"tool_name": "knowledge_search", "result": domain.KnowledgeToolSearchResult{Items: []domain.KnowledgeMatch{{DocumentID: "doc-a", Reference: "chunk-a"}, {DocumentID: "doc-b", Reference: "chunk-b"}}}}}}
	reserved := RunCitationReservations(events)
	events = append(events, domain.RunEvent{ID: "read-b", Type: domain.EventToolCompleted, Payload: map[string]any{"tool_name": "knowledge_read", "tool_call_id": "call-b", "result": domain.KnowledgeToolReadResult{Content: "read B first", Source: reserved[1]}}})
	_, later := AssignRunCitationSources([]domain.RetrievedDocumentChunk{{Document: domain.Document{ID: "doc-c"}, Chunk: domain.DocumentChunk{ID: "chunk-c"}}}, RunCitationReservations(events))
	if later[0].SourceID != "S3" {
		t.Fatalf("later retrieval shifted earlier reservations: %#v", later)
	}
	events = append(events, domain.RunEvent{Type: domain.EventRetrievalCompleted, Payload: map[string]any{"citation_sources": later}})
	for _, source := range RunCitationReservations(events) {
		if source.ChunkID == "chunk-a" && source.SourceID != "S1" {
			t.Fatalf("unread locator was renumbered: %#v", source)
		}
	}
}
