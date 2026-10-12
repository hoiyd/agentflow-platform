package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/identity"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/testsupport/pgfixture"
	"agentflow-platform/apps/api/internal/tool"
	"agentflow-platform/apps/api/internal/tool/filesystem"
	"agentflow-platform/apps/api/internal/tool/policy"
)

// Real Postgres grants/Snapshot and actual filesystem Binding; the model is
// unnecessary here because restoration must reject drift before calling it.
func TestFilesystemFrozenRuntimePostgres(t *testing.T) {
	storage, err := store.NewPostgresStore(pgfixture.DatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Close()
	workspace, err := storage.DefaultWorkspace(t.Context(), identity.SuperUserID)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	config := filepath.Join(t.TempDir(), "roots.json")
	data, _ := json.Marshal(map[string]any{"roots": []filesystem.RootConfig{{ID: "project", WorkspaceID: workspace, Path: root}}})
	if err = os.WriteFile(config, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "report.md"), []byte("retained evidence"), 0600); err != nil {
		t.Fatal(err)
	}
	reads, err := filesystem.New(config, storage)
	if err != nil {
		t.Fatal(err)
	}
	tools := tool.DefaultConfig()
	for _, binding := range reads.Bindings() {
		tools.EnabledTools = append(tools.EnabledTools, binding.Descriptor.Name)
		tools.SecurityPolicy.Rules = append(tools.SecurityPolicy.Rules, policy.Rule{ID: binding.Descriptor.Name, Tool: binding.Descriptor.Name, Action: policy.ActionAllowAndLog, Capability: binding.Descriptor.Security})
	}
	toolsPath := filepath.Join(t.TempDir(), "tools.json")
	if err = tool.SaveConfig(toolsPath, tools); err != nil {
		t.Fatal(err)
	}
	manager, err := tool.NewManager(toolsPath, reads.Bindings()...)
	if err != nil {
		t.Fatal(err)
	}
	runtime := newRuntimeForTest(RuntimeOptions{Store: storage, Tools: manager}, newLocalFallbackOpenAIClientForTest())
	owned, err := storage.ForWorkspace(domain.NewWorkspaceScope(workspace)).CreateAgent(domain.Agent{Name: "File reader", Tools: []string{"file_list", "file_search", "file_read"}})
	if err != nil {
		t.Fatal(err)
	}
	runs := []domain.Run{}
	for _, mode := range []string{ChatModeSingle, ChatModeMultiAgent, ChatModeAutonomous} {
		conversation, err := storage.CreateConversationInWorkspace(workspace, "Frozen filesystem "+mode)
		if err != nil {
			t.Fatal(err)
		}
		var run domain.Run
		switch mode {
		case ChatModeSingle:
			p, e := runtime.PrepareChatRunWithContract(t.Context(), owned.ID, conversation.ID, nil)
			err = e
			run = p.Run
		case ChatModeMultiAgent:
			p, e := runtime.PrepareCollaborationRunWithContract(t.Context(), owned.ID, conversation.ID, nil)
			err = e
			run = p.Run
		case ChatModeAutonomous:
			p, e := runtime.PrepareAutonomousRunWithContract(t.Context(), owned.ID, conversation.ID, nil)
			err = e
			run = p.Run
		}
		if err != nil {
			t.Fatal(err)
		}
		restored, e := runtime.restoreRuntime(run)
		if e != nil {
			t.Fatal(e)
		}
		binding, ok := restored.catalog.Installed("file_read")
		if !ok || binding.Descriptor.JournalMode() != "" || binding.Descriptor.Security.SideEffect != policy.SideEffectNone {
			t.Fatal(binding)
		}
		ctx := event.WithScope(t.Context(), event.Scope{RunID: run.ID, ConversationID: conversation.ID})
		result := tool.NewExecutor(restored.catalog, tool.ExecutorOptions{Authorize: runtime.authorizeTool(run.ID, restored.agent), Tracer: event.NewToolExecutionTracer(event.NewRecorder(storage), run.ID, "")}).Execute(ctx, tool.ExecutionRequest{CallID: "file-round-trip", RunID: run.ID, Tool: "file_read", Arguments: json.RawMessage(`{"root":"project","path":"report.md"}`)})
		if result.Error != nil {
			t.Fatal(result.Error)
		}
		persisted, ok, e := storage.GetRun(run.ID)
		if e != nil || !ok {
			t.Fatal(e)
		}
		snapshot, _ := json.Marshal(persisted.RuntimeSnapshot)
		if strings.Contains(string(snapshot), root) {
			t.Fatal("host path leaked into frozen Snapshot")
		}
		events, e := storage.ListRunEvents(run.ID)
		if e != nil {
			t.Fatal(e)
		}
		evidence, _ := json.Marshal(events)
		if !strings.Contains(string(evidence), "retained evidence") || !strings.Contains(string(evidence), "content_hash") {
			t.Fatal("read evidence was not persisted")
		}
		runs = append(runs, persisted)
	}
	if err = os.WriteFile(config, []byte(`{"roots":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, run := range runs {
		if _, err = runtime.restoreRuntime(run); err == nil || !strings.Contains(err.Error(), "filesystem_root_changed") {
			t.Fatalf("root revocation not checked on Resume: %v", err)
		}
	}
	if err = os.WriteFile(config, data, 0600); err != nil {
		t.Fatal(err)
	}
	// A fresh Catalog after restart reuses the same root identity/revision.
	restarted, err := filesystem.New(config, storage)
	if err != nil {
		t.Fatal(err)
	}
	replacement, err := tool.NewManager(toolsPath, restarted.Bindings()...)
	if err != nil {
		t.Fatal(err)
	}
	runtime = newRuntimeForTest(RuntimeOptions{Store: storage, Tools: replacement}, newLocalFallbackOpenAIClientForTest())
	for _, run := range runs {
		if _, err = runtime.restoreRuntime(run); err != nil {
			t.Fatal(err)
		}
	}
	if err = os.Rename(root, root+"-old"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root + "-old") })
	if err = os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	for _, run := range runs {
		if _, err = runtime.restoreRuntime(run); err == nil {
			t.Fatal("replaced root accepted")
		}
	}
}
