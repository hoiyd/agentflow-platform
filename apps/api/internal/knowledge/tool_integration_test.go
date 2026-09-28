package knowledge

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
	eventpkg "agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/inference/openai"
	"agentflow-platform/apps/api/internal/testsupport/fixturestore"
	"agentflow-platform/apps/api/internal/tool"
)

func TestKnowledgeToolsScopedSearchReadAndPaging(t *testing.T) {
	storage := fixturestore.New()
	base := NewKnowledgeBase(storage, embeddingStub{embedding: openai.Embedding{Vector: []float64{1, 0, 0}, Provider: "test", Model: "embedding-v1", Dimensions: 3}})
	document, err := base.Ingest(t.Context(), domain.DocumentIngestRequest{WorkspaceID: "workspace-a", Title: "Release guide", Content: "Release checkpoints preserve durable state. 恢复时保持一致。", Version: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := storage.CreateConversationInWorkspace("workspace-a", "Knowledge tools")
	if err != nil {
		t.Fatal(err)
	}
	run, err := storage.CreateRunWithContract("agent_planner", conversation.ID, domain.RuntimeSnapshot{SchemaVersion: domain.CurrentRuntimeSnapshotVersion, RunBudget: &domain.RuntimeRunBudget{}, Embedding: domain.RuntimeEmbeddingSnapshot{Provider: "test", Model: "embedding-v1", Dimensions: 3}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := eventpkg.WithScope(t.Context(), eventpkg.Scope{RunID: run.ID, ConversationID: conversation.ID, StageID: "stage-knowledge"})
	catalog, err := tool.NewCatalog(base.ToolBindings(storage)...)
	if err != nil {
		t.Fatal(err)
	}
	executor := tool.NewExecutor(catalog, tool.ExecutorOptions{Tracer: eventpkg.NewToolExecutionTracer(eventpkg.NewRecorder(storage), run.ID, "stage-knowledge")})
	invoke := func(ctx context.Context, name, arguments string) tool.ExecutionResult {
		return executor.Execute(ctx, tool.ExecutionRequest{RunID: run.ID, StageID: "stage-knowledge", CallID: arguments + name, Tool: name, Arguments: json.RawMessage(arguments)})
	}
	search := invoke(ctx, "knowledge_search", `{"query":"release checkpoints"}`)
	if search.Error != nil {
		t.Fatal(search.Error)
	}
	bytes, _ := json.Marshal(search.Result)
	var matches domain.KnowledgeToolSearchResult
	if err := json.Unmarshal(bytes, &matches); err != nil || len(matches.Items) != 1 {
		t.Fatalf("search=%s err=%v", bytes, err)
	}
	ref := matches.Items[0].Reference
	page := invoke(ctx, "knowledge_read", `{"reference":"`+ref+`","limit":16}`)
	if page.Error != nil {
		t.Fatal(page.Error)
	}
	bytes, _ = json.Marshal(page.Result)
	var read domain.KnowledgeToolReadResult
	if err := json.Unmarshal(bytes, &read); err != nil || read.NextOffset != 16 || !read.Truncated || read.Source.DocumentID != document.ID || read.Source.SourceID != "S1" {
		t.Fatalf("read=%s err=%v", bytes, err)
	}
	if result := invoke(ctx, "knowledge_read", `{"reference":"forged"}`); result.Error == nil {
		t.Fatal("guessed reference was accepted")
	}
	if result := invoke(ctx, "knowledge_search", `{"query":"release","workspace_id":"workspace-b"}`); result.Error == nil || result.Error.Code != tool.ErrorInvalidArgs {
		t.Fatal("schema allowed scope expansion")
	}
	if result := invoke(context.Background(), "knowledge_search", `{"query":"release"}`); result.Error == nil || result.Error.Code != tool.ErrorSecurityScopeInvalid {
		t.Fatal("missing scope was accepted")
	}
	if result := invoke(eventpkg.WithScope(ctx, eventpkg.Scope{RunID: run.ID, ConversationID: "another"}), "knowledge_search", `{"query":"release"}`); result.Error == nil || result.Error.Code != tool.ErrorSecurityScopeInvalid {
		t.Fatal("conflicting conversation scope was accepted")
	}
	if err := storage.DeleteDocumentInWorkspace("workspace-a", document.ID); err != nil {
		t.Fatal(err)
	}
	if result := invoke(ctx, "knowledge_read", `{"reference":"`+ref+`"}`); result.Error == nil || !strings.Contains(result.Error.Message, "unavailable") {
		t.Fatal("deleted document remained readable")
	}
}
