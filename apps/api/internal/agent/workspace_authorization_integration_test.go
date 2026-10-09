package agent

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/testsupport/pgfixture"
	"agentflow-platform/apps/api/internal/tool"
	"agentflow-platform/apps/api/internal/tool/policy"
)

// Uses persisted grants and the real Calculator Binding. A frozen catalog alone
// cannot prove that revocation is enforced before a Handler runs.
func TestWorkspaceRuntimeAuthorizationPostgres(t *testing.T) {
	url := pgfixture.DatabaseURL(t)
	storage, err := store.NewPostgresStore(url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	spaces := pgfixture.GrantMemberships(t, url, "https://fixture.invalid", "owner", "A", "B")
	others := pgfixture.GrantMemberships(t, url, "https://fixture.invalid", "other", "C", "D")
	path := filepath.Join(t.TempDir(), "tools.json")
	if err = tool.SaveConfig(path, tool.DefaultConfig()); err != nil {
		t.Fatal(err)
	}
	manager, err := tool.NewManager(path)
	if err != nil {
		t.Fatal(err)
	}
	runtime := newRuntimeForTest(RuntimeOptions{Store: storage, Tools: manager}, newLocalFallbackOpenAIClientForTest())
	agent, err := storage.ForWorkspace(domain.NewWorkspaceScope(spaces[0])).CreateAgent(domain.Agent{Name: "Calculator owner", Tools: []string{"calculator"}})
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := storage.CreateConversationInWorkspace(spaces[0], "Authorized execution")
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := runtime.PrepareChatRunWithContract(t.Context(), agent.ID, conversation.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	frozen, err := runtime.restoreRuntime(prepared.Run)
	if err != nil {
		t.Fatal(err)
	}
	executor := tool.NewExecutor(frozen.catalog, tool.ExecutorOptions{Authorize: runtime.authorizeTool(prepared.Run.ID, frozen.agent)})
	call := tool.ExecutionRequest{RunID: prepared.Run.ID, Tool: "calculator", Arguments: []byte(`{"expression":"2+3"}`)}
	if result := executor.Execute(t.Context(), call); result.Error != nil {
		t.Fatal(result.Error)
	}
	for _, space := range append(spaces[1:], others...) {
		foreign, err := storage.CreateConversationInWorkspace(space, "Foreign Agent")
		if err != nil {
			t.Fatal(err)
		}
		for _, mode := range []string{ChatModeSingle, ChatModeMultiAgent, ChatModeAutonomous} {
			var err error
			switch mode {
			case ChatModeSingle:
				_, err = runtime.PrepareChatRunWithContract(t.Context(), agent.ID, foreign.ID, nil)
			case ChatModeMultiAgent:
				_, err = runtime.PrepareCollaborationRunWithContract(t.Context(), agent.ID, foreign.ID, nil)
			case ChatModeAutonomous:
				_, err = runtime.PrepareAutonomousRunWithContract(t.Context(), agent.ID, foreign.ID, nil)
			}
			if err == nil {
				t.Fatalf("foreign Agent accepted in %s / %s", space, mode)
			}
		}
	}
	assertDenied := func(reason string) {
		t.Helper()
		result := executor.Execute(t.Context(), call)
		if result.Error == nil || result.Error.Code != tool.ErrorSecurityPolicyDenied || (reason != "" && result.PolicyDecision.Reason != reason) {
			t.Fatalf("revocation bypass: %#v", result)
		}
		if reason != "" && !strings.Contains(result.Error.Error(), reason) {
			t.Fatalf("denial lost its explanation: %v", result.Error)
		}
	}
	if err = storage.SetWorkspaceToolEnabled(spaces[0], "calculator", false); err != nil {
		t.Fatal(err)
	}
	assertDenied("workspace_disabled")
	restored, err := runtime.restoreRuntime(prepared.Run)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := restored.catalog.ResolveReady("calculator"); ok {
		t.Fatal("Resume restored revoked Workspace grant")
	}
	if err = storage.SetWorkspaceToolEnabled(spaces[0], "calculator", true); err != nil {
		t.Fatal(err)
	}
	if _, err = manager.SetEnabled("calculator", false); err != nil {
		t.Fatal(err)
	}
	assertDenied("service_disabled")
	if _, err = manager.SetEnabled("calculator", true); err != nil {
		t.Fatal(err)
	}
	forged := call
	forged.Tool = "get_current_time"
	if err := runtime.authorizeTool(prepared.Run.ID, frozen.agent)(t.Context(), forged, policy.Scope{}); err == nil {
		t.Fatal("unselected Tool authorized")
	}
	agent.Tools = nil
	if _, err = storage.UpdateAgent(agent); err != nil {
		t.Fatal(err)
	}
	assertDenied("agent_disabled")
	agent.Tools = []string{"calculator"}
	if _, err = storage.UpdateAgent(agent); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`DELETE FROM auth_memberships WHERE workspace_id=$1`, spaces[0]); err != nil {
		t.Fatal(err)
	}
	assertDenied("")
	if _, err = runtime.restoreRuntime(prepared.Run); err == nil {
		t.Fatal("Resume bypassed revoked Membership")
	}
	if _, err = db.Exec(`INSERT INTO auth_memberships(user_id,workspace_id) SELECT owner_user_id,id FROM workspaces WHERE id=$1`, spaces[0]); err != nil {
		t.Fatal(err)
	}
	if err = storage.ArchiveAgent(agent.ID); err != nil {
		t.Fatal(err)
	}
	assertDenied("")
	if _, err = runtime.restoreRuntime(prepared.Run); err == nil {
		t.Fatal("Resume used archived Agent")
	}
}
