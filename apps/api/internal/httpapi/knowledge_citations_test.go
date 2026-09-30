package httpapi

import (
	"strings"
	"testing"

	"agentflow-platform/apps/api/app/runcompletion"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/testsupport/fixturestore"
)

func TestKnowledgeReadGroundingUsesOnlyDeliveredPages(t *testing.T) {
	storage := fixturestore.New()
	conversation, _ := storage.CreateConversation("read evidence")
	run, err := storage.CreateRunWithContract("agent_planner", conversation.ID, testRuntimeSnapshot(), nil)
	if err != nil {
		t.Fatal(err)
	}
	source := domain.RAGCitation{SourceID: "S1", DocumentID: "doc-1", ChunkID: "chunk-1"}
	for _, content := range []string{"First selected page.", "Second selected page."} {
		callID := content
		_, err := storage.CreateRunEvent(domain.RunEvent{RunID: run.ID, Type: domain.EventToolCompleted, Payload: map[string]any{
			"tool_name": "knowledge_read", "tool_call_id": callID, "result": domain.KnowledgeToolReadResult{Content: content, Source: source},
		}})
		if err != nil {
			t.Fatal(err)
		}
	}
	_, _ = storage.CreateRunEvent(domain.RunEvent{RunID: run.ID, Type: domain.EventContextAssembled, Payload: map[string]any{"manifest": domain.ContextManifest{Entries: []domain.ContextManifestEntry{
		{Source: "tool_result", ReferenceID: "First selected page.", CitationSourceID: "S1", Selected: true, Transformation: "original", OriginalBytes: 100, IncludedBytes: 100},
		{Source: "knowledge", ReferenceID: "Second selected page.", CitationSourceID: "S1", Selected: true, Transformation: "knowledge_read_wrapped", OriginalBytes: 21, IncludedBytes: 21},
	}}}})
	scoped := storage.ForWorkspace(domain.NewWorkspaceScope(domain.DefaultWorkspaceID))
	sources, err := runcompletion.GroundingSources(scoped, run.ID)
	if err != nil || len(sources) != 1 || !strings.Contains(sources[0].Content, "First selected page.") || !strings.Contains(sources[0].Content, "Second selected page.") {
		t.Fatalf("sources=%#v err=%v", sources, err)
	}
}

func TestKnowledgeReadDoesNotReplaceSelectedAutomaticGrounding(t *testing.T) {
	storage := fixturestore.New()
	conversation, _ := storage.CreateConversation("shared evidence")
	run, err := storage.CreateRunWithContract("agent_planner", conversation.ID, testRuntimeSnapshot(), nil)
	if err != nil {
		t.Fatal(err)
	}
	document, err := storage.CreateDocument(domain.Document{ID: "doc-1", Title: "Facts", Version: "v1", Content: "Selected automatic facts."}, []domain.DocumentChunk{{ID: "chunk-1", DocumentID: "doc-1", Content: "Selected automatic facts."}}, []domain.DocumentChunkEmbedding{{Embedding: []float64{1}}})
	if err != nil {
		t.Fatal(err)
	}
	source := domain.RAGCitation{SourceID: "S1", DocumentID: document.ID, DocumentVersion: document.Version, ChunkID: "chunk-1"}
	_, _ = storage.CreateRunEvent(domain.RunEvent{RunID: run.ID, Type: domain.EventRetrievalCompleted, Payload: map[string]any{"citation_sources": []domain.RAGCitation{source}}})
	_, _ = storage.CreateRunEvent(domain.RunEvent{RunID: run.ID, Type: domain.EventToolCompleted, Payload: map[string]any{"tool_name": "knowledge_read", "tool_call_id": "call-read", "result": domain.KnowledgeToolReadResult{Content: "Selected", Source: source}}})
	_, _ = storage.CreateRunEvent(domain.RunEvent{RunID: run.ID, Type: domain.EventContextAssembled, Payload: map[string]any{"manifest": domain.ContextManifest{Entries: []domain.ContextManifestEntry{
		{Source: "knowledge", ReferenceID: "chunk-1", CitationSourceID: "S1", Selected: true},
		{Source: "tool_result", ReferenceID: "call-read", CitationSourceID: "S1", Selected: true, Transformation: "original"},
	}}}})
	sources, err := runcompletion.GroundingSources(storage.ForWorkspace(domain.NewWorkspaceScope(domain.DefaultWorkspaceID)), run.ID)
	if err != nil || len(sources) != 1 || !strings.Contains(sources[0].Content, "automatic facts") {
		t.Fatalf("sources=%#v err=%v", sources, err)
	}
}

func TestKnowledgeReadCitationsRequireSelectedIntactPage(t *testing.T) {
	for _, variant := range []string{"excluded", "compacted", "spilled", "unknown source", "search only"} {
		t.Run(variant, func(t *testing.T) {
			storage := fixturestore.New()
			conversation, _ := storage.CreateConversation("excluded evidence")
			run, err := storage.CreateRunWithContract("agent_planner", conversation.ID, testRuntimeSnapshot(), nil)
			if err != nil {
				t.Fatal(err)
			}
			source := domain.RAGCitation{SourceID: "S1", DocumentID: "doc-1", ChunkID: "chunk-1"}
			payload := map[string]any{"tool_name": "knowledge_read", "tool_call_id": "call-read", "result": domain.KnowledgeToolReadResult{Content: "Facts.", Source: source}}
			entry := domain.ContextManifestEntry{Source: "tool_result", ReferenceID: "call-read", CitationSourceID: "S1", Selected: true, Transformation: "original", OriginalBytes: 100, IncludedBytes: 100}
			switch variant {
			case "excluded":
				entry.Selected = false
			case "compacted":
				entry.Transformation = "tool_result_compacted"
				entry.IncludedBytes = 20
			case "spilled":
				payload["truncated"] = true
			case "unknown source":
				entry.CitationSourceID = "S99"
			case "search only":
				payload["tool_name"] = "knowledge_search"
			}
			_, _ = storage.CreateRunEvent(domain.RunEvent{RunID: run.ID, Type: domain.EventToolCompleted, Payload: payload})
			_, _ = storage.CreateRunEvent(domain.RunEvent{RunID: run.ID, Type: domain.EventContextAssembled, Payload: map[string]any{"manifest": domain.ContextManifest{Entries: []domain.ContextManifestEntry{entry}}}})
			_, citations, invalid, err := runcompletion.ResolveCitations(storage.ForWorkspace(domain.NewWorkspaceScope(domain.DefaultWorkspaceID)), run.ID, "Facts [S1].")
			if err != nil || len(citations) != 0 || len(invalid) != 1 {
				t.Fatalf("citations=%#v invalid=%#v err=%v", citations, invalid, err)
			}
		})
	}
}
