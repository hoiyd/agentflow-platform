//go:build linux || darwin

package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
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

// Actual filesystem/Executor/Postgres, not a callback returning a canned preview.
func TestFileUndoHTTPContractAndWorkspaceBoundary(t *testing.T) {
	storage, err := store.NewPostgresStore(pgfixture.DatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { storage.Close() })
	workspace, err := storage.DefaultWorkspace(t.Context(), identity.SuperUserID)
	if err != nil {
		t.Fatal(err)
	}
	root, config := t.TempDir(), filepath.Join(t.TempDir(), "roots.json")
	if err = os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	data := fmt.Sprintf(`{"roots":[{"id":"output","workspace_id":%q,"path":%q,"write_access":"managed_output","undo_retention_seconds":3600,"undo_max_bytes":1048576}]}`, workspace, root)
	if err = os.WriteFile(config, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	files, err := filesystem.New(config, storage)
	if err != nil {
		t.Fatal(err)
	}
	rules := []policy.Rule{}
	for _, b := range files.Bindings() {
		rules = append(rules, policy.Rule{ID: b.Descriptor.Name, Tool: b.Descriptor.Name, Action: policy.ActionAllowAndLog, Capability: b.Descriptor.Security})
	}
	catalog, err := tool.NewCatalogWithPolicy(policy.Policy{DefaultAction: policy.ActionDeny, Rules: rules}, files.Bindings()...)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = storage.EnsureWorkspaceToolConfig(workspace, []string{"file_write", "file_patch", "file_read"}); err != nil {
		t.Fatal(err)
	}
	conversation, err := storage.CreateConversationInWorkspace(workspace, "Undo HTTP evidence")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := storage.ForWorkspace(domain.NewWorkspaceScope(workspace)).CreateAgent(domain.Agent{Name: "HTTP writer", Tools: []string{"file_write"}})
	if err != nil {
		t.Fatal(err)
	}
	run, err := storage.CreateRunWithContract(agent.ID, conversation.ID, domain.RuntimeSnapshot{SchemaVersion: domain.CurrentRuntimeSnapshotVersion, RunBudget: &domain.RuntimeRunBudget{}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := event.WithScope(t.Context(), event.Scope{RunID: run.ID, ConversationID: run.ConversationID, TurnID: "http-undo-turn"})
	executor := tool.NewExecutor(catalog, tool.ExecutorOptions{EffectJournal: storage, Tracer: event.NewToolExecutionTracer(event.NewRecorder(storage), run.ID, "")})
	first := executor.Execute(ctx, tool.ExecutionRequest{RunID: run.ID, TurnID: "http-undo-turn", CallID: "create", Tool: "file_write", Arguments: json.RawMessage(`{"root":"output","path":"report.md","content":"Private retained report."}`)})
	if first.Error != nil {
		t.Fatal(first.Error)
	}
	result, _ := json.Marshal(first.Result)
	var receipt filesystem.WriteResult
	if err = json.Unmarshal(result, &receipt); err != nil {
		t.Fatal(err)
	}
	second := executor.Execute(ctx, tool.ExecutionRequest{RunID: run.ID, TurnID: "http-undo-turn", CallID: "replace", Tool: "file_write", Arguments: json.RawMessage(fmt.Sprintf(`{"root":"output","path":"report.md","content":"Public updated report.","expected_hash":%q}`, receipt.NewHash))})
	if second.Error != nil {
		t.Fatal(second.Error)
	}
	if _, err = storage.UpdateRunStatus(run.ID, domain.RunCompleted, ""); err != nil {
		t.Fatal(err)
	}
	records, err := storage.ListToolEffects(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	var effect domain.ToolEffectRecord
	for _, r := range records {
		if r.ToolCallID == "replace" {
			effect = r
		}
	}
	handler := &Handler{store: storage, tools: recoveryCatalog{catalog}}
	base := "/api/runs/" + run.ID + "/tool-effects/" + effect.IdempotencyKey
	get := func(path, scope string, status int) []byte {
		t.Helper()
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set(WorkspaceHeader, scope)
		w := httptest.NewRecorder()
		handler.Routes().ServeHTTP(w, r)
		if w.Code != status {
			t.Fatalf("GET %s = %d: %s", path, w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "Private retained report.") || strings.Contains(w.Body.String(), root) {
			t.Fatal("private content or host path in response")
		}
		return w.Body.Bytes()
	}
	var preview tool.CompensationPreview
	if err = json.Unmarshal(get(base+"/compensation-preview", workspace, 200), &preview); err != nil || !preview.Available || preview.Resource != "output/report.md" || preview.BeforeBytes != len("Private retained report.") {
		t.Fatal(preview, err)
	}
	get(base+"/compensation-preview", "999999", 404)
	get("/api/runs/missing/tool-effects/effect/compensation-preview", workspace, 404)
	get("/api/runs/"+run.ID+"/tool-effects/missing/compensation-preview", workspace, 404)
	if err = storage.SetWorkspaceToolEnabled(workspace, "file_write", false); err != nil {
		t.Fatal(err)
	}
	get(base+"/compensation-preview", workspace, 409)
	if err = storage.SetWorkspaceToolEnabled(workspace, "file_write", true); err != nil {
		t.Fatal(err)
	}
	for _, fault := range []string{"run-read", "effect-read", "grant-read"} {
		broken := &Handler{store: undoHTTPStoreFailure{Store: storage, fault: fault}, tools: recoveryCatalog{catalog}}
		r := httptest.NewRequest("GET", base+"/compensation-preview", nil)
		r.Header.Set(WorkspaceHeader, workspace)
		w := httptest.NewRecorder()
		broken.Routes().ServeHTTP(w, r)
		if w.Code != 500 || strings.Contains(w.Body.String(), `"available":true`) {
			t.Fatal("database failure permitted Undo", fault, w.Code, w.Body.String())
		}
	}
	command := fmt.Sprintf(`{"command_id":"http-undo","action":"compensate","expected_version":%d,"actor":"forged-user","reason":"Restore report"}`, effect.Version)
	r := httptest.NewRequest("POST", base+"/reconcile", strings.NewReader(command))
	r.Header.Set(WorkspaceHeader, workspace)
	w := httptest.NewRecorder()
	handler.Routes().ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"compensated"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, key := range []string{"", strings.Repeat("x", 257)} {
		r := httptest.NewRequest("GET", "/", nil)
		r.SetPathValue("id", run.ID)
		r.SetPathValue("idempotency_key", key)
		w := httptest.NewRecorder()
		handler.toolCompensationPreview(w, r)
		if w.Code != 400 {
			t.Fatal(w.Code)
		}
	}
	events, err := storage.ListRunEvents(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range events {
		if item.Type == domain.EventToolEffectReconciled && item.Payload["actor"] != identity.SuperUserID {
			t.Fatal("client controlled audit actor", item)
		}
	}
	if data, err := os.ReadFile(filepath.Join(root, "report.md")); err != nil || string(data) != "Private retained report." {
		t.Fatal(string(data), err)
	}
}

type undoHTTPStoreFailure struct {
	store.Store
	fault string
}

func (s undoHTTPStoreFailure) ForWorkspace(scope domain.WorkspaceScope) store.WorkspaceStore {
	return undoHTTPScopedFailure{WorkspaceStore: s.Store.ForWorkspace(scope), fault: s.fault}
}
func (s undoHTTPStoreFailure) EnsureWorkspaceToolConfig(workspace string, names []string) (domain.WorkspaceToolConfig, error) {
	if s.fault == "grant-read" {
		return domain.WorkspaceToolConfig{}, errors.New("grant database unavailable")
	}
	return s.Store.EnsureWorkspaceToolConfig(workspace, names)
}

type undoHTTPScopedFailure struct {
	store.WorkspaceStore
	fault string
}

func (s undoHTTPScopedFailure) GetRun(id string) (domain.Run, bool, error) {
	if s.fault == "run-read" {
		return domain.Run{}, false, errors.New("run database unavailable")
	}
	return s.WorkspaceStore.GetRun(id)
}
func (s undoHTTPScopedFailure) ListToolEffects(id string) ([]domain.ToolEffectRecord, error) {
	if s.fault == "effect-read" {
		return nil, errors.New("effect database unavailable")
	}
	return s.WorkspaceStore.ListToolEffects(id)
}
