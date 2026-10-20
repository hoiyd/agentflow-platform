package agent

import (
	"errors"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/rag"
	"agentflow-platform/apps/api/internal/rag/eligibility"
)

// Failure inventory: completed Stage output can contain withdrawn sources.
// Reject before claiming recovery or executing another Stage; preserve old events.
func TestRecoveryRejectsWithdrawnKnowledge(t *testing.T) {
	for _, locatorOnly := range []bool{false, true} {
		t.Run(map[bool]string{false: "retrieved_evidence", true: "search_preview"}[locatorOnly], func(t *testing.T) { recoveryRejectsWithdrawnKnowledge(t, locatorOnly) })
	}
}

func recoveryRejectsWithdrawnKnowledge(t *testing.T, locatorOnly bool) {
	runtime, s, run := newStageRecoverableCollaboration(t)
	createCompletedStage(t, runtime, run, "planner", "agent_planner", "task", "plan")
	createCompletedStage(t, runtime, run, "router", "agent_planner", "route", "agent_planner")
	doc, chunks, err := rag.BuildDocument(domain.DocumentIngestRequest{WorkspaceID: run.WorkspaceID, Title: "Guide", Content: "ROLLBACK-42 recovery guide."})
	if err != nil {
		t.Fatal(err)
	}
	embeddings := make([]domain.DocumentChunkEmbedding, len(chunks))
	for i := range embeddings {
		embeddings[i] = domain.DocumentChunkEmbedding{Provider: "test", Model: "test", Dimensions: 3, Embedding: []float64{1, 0, 0}}
	}
	doc, err = s.CreateDocument(doc, chunks, embeddings)
	if err != nil {
		t.Fatal(err)
	}
	_, chunks, _, err = s.GetDocument(doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	source := domain.RAGCitation{SourceID: "S1", DocumentID: doc.ID, DocumentVersion: doc.Version, ChunkID: chunks[0].ID}
	item := domain.RunEvent{RunID: run.ID, Type: domain.EventRetrievalCompleted, Payload: map[string]any{"citation_sources": []domain.RAGCitation{source}}}
	if locatorOnly {
		item.Type = domain.EventToolCompleted
		item.Payload = map[string]any{"tool_name": domain.KnowledgeSearchToolName, "tool_call_id": "search-1", "result": domain.KnowledgeToolSearchResult{Items: []domain.KnowledgeMatch{{Reference: chunks[0].ID, DocumentID: doc.ID, DocumentVersion: doc.Version, Preview: "ROLLBACK-42 recovery guide."}}}}
	}
	_, err = s.CreateRunEvent(item)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateDocumentMetadata(run.WorkspaceID, doc.ID, domain.DocumentMetadataUpdate{ExpectedUpdatedAt: doc.UpdatedAt, Metadata: doc.Metadata, Lifecycle: &domain.DocumentLifecycle{Status: "withdrawn"}}); err != nil {
		t.Fatal(err)
	}
	before, err := s.ListRunEvents(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	events, errs := runtime.ResumeRecoverableCollaboration(t.Context(), run.ID)
	for range events {
	}
	if err := <-errs; !errors.Is(err, eligibility.ErrSourceUnavailable) {
		t.Fatalf("expected unavailable knowledge, got %v", err)
	}
	after, _ := s.ListRunEvents(run.ID)
	current, _, _ := s.GetRun(run.ID)
	if len(after) != len(before) || current.Status != domain.RunFailedRecoverable {
		t.Fatal("rejected recovery mutated durable history or Run state")
	}
}
