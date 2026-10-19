package knowledge

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/inference/openai"
	"agentflow-platform/apps/api/internal/testsupport/fixturestore"
	"agentflow-platform/apps/api/internal/tool"
)

// Failure inventory: runtime-knowledge-filters.md. Real Binding, executor,
// persisted Run/search receipt and metadata mutation, not a mocked Tool result.
func TestKnowledgeToolsFrozenFilterAndStalePages(t *testing.T) {
	s := fixturestore.New()
	base := NewKnowledgeBase(s, embeddingStub{embedding: openai.Embedding{Vector: []float64{1, 0, 0}, Provider: "test", Model: "embedding-v1", Dimensions: 3}})
	var eligible domain.Document
	for _, product := range []string{"AgentFlow", "Other"} {
		doc, err := base.Ingest(t.Context(), domain.DocumentIngestRequest{WorkspaceID: "1", Title: product, Content: "ROLLBACK-742 checkpoints preserve durable state.", MetadataSchemaVersion: "knowledge-metadata-v1", Metadata: map[string]any{"product": product}})
		if err != nil {
			t.Fatal(err)
		}
		if product == "AgentFlow" {
			eligible = doc
		}
	}
	conv, err := s.CreateConversationInWorkspace("1", "Frozen filter")
	if err != nil {
		t.Fatal(err)
	}
	filter := json.RawMessage(`{"schema_version":"knowledge-metadata-v1","where":{"field":"document.metadata.product","op":"eq","value":"AgentFlow"}}`)
	hash := fmt.Sprintf("sha256:%x", sha256.Sum256(filter))
	var snapshot domain.RuntimeSnapshot
	if err := json.Unmarshal([]byte(fmt.Sprintf(`{"schema_version":18,"embedding":{"provider":"test","model":"embedding-v1","dimensions":3},"run_budget":{},"knowledge_scope":{"filter":%s,"id":%q,"policy_version":"knowledge-filter-v1","source":"run_request"}}`, filter, hash)), &snapshot); err != nil {
		t.Fatal(err)
	}
	run, err := s.CreateRunWithContract("agent_planner", conv.ID, snapshot, nil)
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
	if json.Unmarshal(data, &matches) != nil || len(matches.Items) != 1 || matches.Items[0].DocumentID != eligible.ID {
		t.Fatalf("frozen scope lost: %s", data)
	}
	ref := matches.Items[0].Reference
	if result := invoke("read-1", "knowledge_read", fmt.Sprintf(`{"reference":%q,"limit":16}`, ref)); result.Error != nil {
		t.Fatal(result.Error)
	}
	if _, err := base.UpdateMetadata(t.Context(), "1", eligible.ID, domain.DocumentMetadataUpdate{ExpectedUpdatedAt: eligible.UpdatedAt, Metadata: map[string]any{"product": "Other"}}); err != nil {
		t.Fatal(err)
	}
	if result := invoke("read-2", "knowledge_read", fmt.Sprintf(`{"reference":%q,"offset":16}`, ref)); result.Error == nil {
		t.Fatal("label mutation bypassed frozen filter on next page")
	}
	t.Logf("runtime-filter-tool-evidence run=%s workspace=1 filter=%s eligible=%s read_before=true read_after_metadata_change=false", run.ID, filter, eligible.ID)
}
