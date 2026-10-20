package knowledge

import (
	"encoding/json"
	"fmt"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/inference/openai"
	"agentflow-platform/apps/api/internal/testsupport/fixturestore"
	"agentflow-platform/apps/api/internal/tool"
)

// Failure inventory: a previously issued reference is not permanent permission;
// withdrawal must deny later pages even when the Run has no metadata filter.
func TestKnowledgeReadRechecksLifecycle(t *testing.T) {
	s := fixturestore.New()
	base := NewKnowledgeBase(s, embeddingStub{embedding: openai.Embedding{Vector: []float64{1, 0, 0}, Provider: "test", Model: "embedding-v1", Dimensions: 3}})
	doc, err := base.Ingest(t.Context(), domain.DocumentIngestRequest{WorkspaceID: "1", Title: "Guide", Content: "ROLLBACK-742 checkpoints preserve durable state."})
	if err != nil {
		t.Fatal(err)
	}
	conv, err := s.CreateConversationInWorkspace("1", "Lifecycle")
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.CreateRunWithContract("agent_planner", conv.ID, domain.RuntimeSnapshot{SchemaVersion: domain.CurrentRuntimeSnapshotVersion, RunBudget: &domain.RuntimeRunBudget{}, Embedding: domain.RuntimeEmbeddingSnapshot{Provider: "test", Model: "embedding-v1", Dimensions: 3}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := event.WithScope(t.Context(), event.Scope{RunID: run.ID, ConversationID: conv.ID})
	catalog, err := tool.NewCatalog(base.ToolBindings(s)...)
	if err != nil {
		t.Fatal(err)
	}
	executor := tool.NewExecutor(catalog, tool.ExecutorOptions{Tracer: event.NewToolExecutionTracer(event.NewRecorder(s), run.ID, "")})
	invoke := func(id, name, args string) tool.ExecutionResult {
		return executor.Execute(ctx, tool.ExecutionRequest{RunID: run.ID, CallID: id, Tool: name, Arguments: json.RawMessage(args)})
	}
	result := invoke("search", "knowledge_search", `{"query":"ROLLBACK-742"}`)
	if result.Error != nil {
		t.Fatal(result.Error)
	}
	data, _ := json.Marshal(result.Result)
	var matches domain.KnowledgeToolSearchResult
	if json.Unmarshal(data, &matches) != nil || len(matches.Items) != 1 {
		t.Fatalf("search: %s", data)
	}
	args := fmt.Sprintf(`{"reference":%q,"limit":16}`, matches.Items[0].Reference)
	if result := invoke("read", "knowledge_read", args); result.Error != nil {
		t.Fatal(result.Error)
	}
	if _, err := base.UpdateMetadata(t.Context(), "1", doc.ID, domain.DocumentMetadataUpdate{ExpectedUpdatedAt: doc.UpdatedAt, Metadata: doc.Metadata, Lifecycle: &domain.DocumentLifecycle{Status: "withdrawn"}}); err != nil {
		t.Fatal(err)
	}
	if result := invoke("stale-read", "knowledge_read", args); result.Error == nil {
		t.Fatal("withdrawn reference remained readable")
	}
}
