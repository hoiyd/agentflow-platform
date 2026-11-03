package agent

import (
	"reflect"
	"slices"
	"testing"

	"agentflow-platform/apps/api/internal/clarification"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/knowledge"
	"agentflow-platform/apps/api/internal/skill"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/taskstate"
	"agentflow-platform/apps/api/internal/testsupport/pgfixture"
	"agentflow-platform/apps/api/internal/tool"
	"agentflow-platform/apps/api/internal/tool/artifact"
)

// Failure inventory: registration must not grant unbound Skills, duplicate names,
// expose parent-only control to Workers, or bypass current Workspace revocation.
func TestHarnessRegistrationPreservesAutomaticToolsAndRevocation(t *testing.T) {
	storage, err := store.NewPostgresStore(pgfixture.DatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	client := newLocalFallbackOpenAIClientForTest()
	runtime := newRuntimeForTest(RuntimeOptions{Store: storage, Clarifications: storage, KnowledgeTools: knowledge.NewKnowledgeBase(storage, client).ToolBindings(storage)}, client)
	want := []string{clarification.ToolName, taskstate.UpdateToolName, artifact.ReadToolName, artifact.SearchToolName, domain.KnowledgeSearchToolName, domain.KnowledgeReadToolName}
	if got := runtime.withHarnessTools(nil); !reflect.DeepEqual(got, want) {
		t.Fatalf("automatic names/order=%v want=%v", got, want)
	}
	existing := []string{"calculator", taskstate.UpdateToolName}
	wantExisting := append([]string(nil), existing...)
	for _, name := range want {
		if !slices.Contains(wantExisting, name) {
			wantExisting = append(wantExisting, name)
		}
	}
	if got := runtime.withHarnessTools(existing); !reflect.DeepEqual(got, wantExisting) || !reflect.DeepEqual(existing, []string{"calculator", taskstate.UpdateToolName}) {
		t.Fatalf("automatic names changed caller input/order: %v", got)
	}
	conversation, err := storage.CreateConversation("Synthetic Harness registration")
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := runtime.PrepareChatRunWithContract(t.Context(), "agent_planner", conversation.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	installed, err := runtime.currentCatalog(prepared.Run.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{skill.LoadToolName, skill.ReadToolName} {
		if _, ok := installed.Installed(name); !ok || slices.Contains(prepared.Agent.Tools, name) {
			t.Fatalf("registration and Skill activation authority conflated for %s", name)
		}
	}
	workerCatalog, err := catalogForAgent(prepared.Catalog, prepared.Agent)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{clarification.ToolName, taskstate.UpdateToolName} {
		if _, ok := workerCatalog.ResolveReady(name); ok {
			t.Fatalf("Worker gained parent-owned tool %s", name)
		}
	}
	binding, ok := prepared.Catalog.ResolveReady(artifact.ReadToolName)
	if !ok {
		t.Fatal("expected artifact Binding")
	}
	call := tool.ExecutionRequest{RunID: prepared.Run.ID, Tool: artifact.ReadToolName, DefinitionRevision: binding.Descriptor.DefinitionRevision}
	authorize := runtime.authorizeTool(prepared.Run.ID, prepared.Agent)
	if err := authorize(t.Context(), call, binding.Descriptor.Security.Scope); err != nil {
		t.Fatal(err)
	}
	if err := storage.SetWorkspaceToolEnabled(prepared.Run.WorkspaceID, artifact.ReadToolName, false); err != nil {
		t.Fatal(err)
	}
	if err := authorize(t.Context(), call, binding.Descriptor.Security.Scope); err == nil {
		t.Fatal("automatic registration bypassed current Workspace revocation")
	}
	next, err := runtime.PrepareChatRunWithContract(t.Context(), prepared.Agent.ID, conversation.ID, nil)
	if err != nil || slices.Contains(next.Agent.Tools, artifact.ReadToolName) {
		t.Fatalf("new Run retained revoked Tool: %+v %v", next.Agent.Tools, err)
	}
	t.Logf("run=%s workspace=%s automatic=%v checks=registration-order,deduplication,conditional-skills,worker-isolation,current-revocation limitations=no-live-model", prepared.Run.ID, prepared.Run.WorkspaceID, want)
}
