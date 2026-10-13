//go:build linux || darwin

package filesystem

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/identity"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/testsupport/pgfixture"
	"agentflow-platform/apps/api/internal/tool"
	"agentflow-platform/apps/api/internal/tool/policy"
	"agentflow-platform/apps/api/internal/tool/reconciliation"
)

type writeFixtureState struct {
	root, config, database string
	store                  *store.PostgresStore
	service                *Service
	catalog                *tool.Catalog
	run                    domain.Run
	ctx                    context.Context
}

func newWriteFixture(t *testing.T) writeFixtureState {
	t.Helper()
	database := pgfixture.DatabaseURL(t)
	storage, err := store.NewPostgresStore(database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { storage.Close() })
	workspace, err := storage.DefaultWorkspace(t.Context(), identity.SuperUserID)
	if err != nil {
		t.Fatal(err)
	}
	root, config := t.TempDir(), filepath.Join(t.TempDir(), "roots.json")
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, config, `{"roots":[{"id":"output","workspace_id":`+quoted(workspace)+`,"path":`+quoted(root)+`,"write_access":"managed_output"}]}`)
	service, err := New(config, storage)
	if err != nil {
		t.Fatal(err)
	}
	rules := []policy.Rule{}
	for _, binding := range service.Bindings() {
		rules = append(rules, policy.Rule{ID: binding.Descriptor.Name, Tool: binding.Descriptor.Name, Action: policy.ActionAllowAndLog, Capability: binding.Descriptor.Security})
	}
	catalog, err := tool.NewCatalogWithPolicy(policy.Policy{Version: "write-fixture", DefaultAction: policy.ActionDeny, Rules: rules}, service.Bindings()...)
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := storage.CreateConversationInWorkspace(workspace, "Report evidence")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := storage.ForWorkspace(domain.NewWorkspaceScope(workspace)).CreateAgent(domain.Agent{Name: "Writer", Tools: []string{"file_read", "file_write", "file_patch"}})
	if err != nil {
		t.Fatal(err)
	}
	run, err := storage.CreateRunWithContract(agent.ID, conversation.ID, domain.RuntimeSnapshot{SchemaVersion: domain.CurrentRuntimeSnapshotVersion, RunBudget: &domain.RuntimeRunBudget{}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := event.WithScope(t.Context(), event.Scope{RunID: run.ID, ConversationID: conversation.ID, TurnID: "write-turn"})
	return writeFixtureState{root: root, config: config, database: database, store: storage, service: service, catalog: catalog, run: run, ctx: ctx}
}

func (f writeFixtureState) execute(t *testing.T, name, call, args string) tool.ExecutionResult {
	t.Helper()
	executor := tool.NewExecutor(f.catalog, tool.ExecutorOptions{EffectJournal: f.store, Tracer: event.NewToolExecutionTracer(event.NewRecorder(f.store), f.run.ID, "")})
	return executor.Execute(f.ctx, tool.ExecutionRequest{RunID: f.run.ID, TurnID: "write-turn", CallID: call, Tool: name, Arguments: json.RawMessage(args)})
}

func TestFilesystemWriteReportPatchReplayAndReconciliation(t *testing.T) {
	f := newWriteFixture(t)
	args := `{"root":"output","path":"report.md","content":"Evidence: original report."}`
	first := resultMap(t, f.execute(t, "file_write", "create", args))
	if first["operation"] != "create" || first["new_hash"] == "" {
		t.Fatal(first)
	}
	if replay := f.execute(t, "file_write", "create", args); replay.Error != nil || !replay.Replayed {
		t.Fatal(replay)
	}
	read := resultMap(t, f.execute(t, "file_read", "read", `{"root":"output","path":"report.md"}`))
	if read["content"] != "Evidence: original report." {
		t.Fatal(read)
	}
	patch := `{"root":"output","path":"report.md","expected_hash":` + quoted(read["content_hash"].(string)) + `,"old_text":"original","new_text":"verified"}`
	updated := resultMap(t, f.execute(t, "file_patch", "patch", patch))
	if updated["old_hash"] != read["content_hash"] || updated["new_hash"] == read["content_hash"] {
		t.Fatal(updated)
	}
	if data, _ := os.ReadFile(filepath.Join(f.root, "report.md")); string(data) != "Evidence: verified report." {
		t.Fatal(string(data))
	}
	records, err := f.store.ListToolEffects(f.run.ID)
	if err != nil || len(records) != 2 {
		t.Fatal(records, err)
	}
	for _, record := range records {
		if record.Status != domain.ToolEffectCommitted || record.StageID != "" || record.TurnID != "write-turn" || strings.Contains(string(record.Result), f.root) {
			t.Fatal(record)
		}
	}
	// Crash window: the file is published but the shared journal commit fails.
	executor := tool.NewExecutor(f.catalog, tool.ExecutorOptions{EffectJournal: failedCommitJournal{f.store}, Tracer: event.NewToolExecutionTracer(event.NewRecorder(f.store), f.run.ID, "")})
	request := tool.ExecutionRequest{RunID: f.run.ID, TurnID: "write-turn", CallID: "crash", Tool: "file_write", Arguments: json.RawMessage(`{"root":"output","path":"recovery.txt","content":"Durable evidence"}`)}
	if got := executor.Execute(f.ctx, request); got.Error == nil || got.Error.Code != tool.ErrorEffectJournal {
		t.Fatal(got)
	}
	records, _ = f.store.ListToolEffects(f.run.ID)
	var pending domain.ToolEffectRecord
	for _, r := range records {
		if r.ToolCallID == "crash" {
			pending = r
		}
	}
	if pending.Status != domain.ToolEffectNeedsReconciliation || !strings.Contains(string(pending.Result), "new_hash") {
		t.Fatal(pending)
	}
	restarted, err := New(f.config, f.store)
	if err != nil {
		t.Fatal(err)
	}
	bindings := restarted.Bindings()
	rebuilt, err := tool.NewCatalogWithPolicy(f.catalog.SecurityPolicy(), bindings...)
	if err != nil {
		t.Fatal(err)
	}
	command := reconciliation.ToolEffectReconciliationCommand{CommandID: "verify-published", Action: domain.ToolEffectRetrySameKey, ExpectedVersion: pending.Version, Actor: "filesystem-test-operator", Reason: "Verify durable publication without rewriting"}
	outcome, err := reconciliation.ReconcileToolEffect(f.ctx, rebuilt, f.store, f.run, pending.IdempotencyKey, command)
	if err != nil || outcome.Effect.Status != domain.ToolEffectCommitted {
		t.Fatal(outcome, err)
	}
	if _, err = reconciliation.ReconcileToolEffect(f.ctx, rebuilt, f.store, f.run, pending.IdempotencyKey, command); err != nil {
		t.Fatal(err)
	}
	if got := tool.NewExecutor(rebuilt, tool.ExecutorOptions{EffectJournal: f.store, Tracer: event.NewToolExecutionTracer(event.NewRecorder(f.store), f.run.ID, "")}).Execute(f.ctx, request); got.Error != nil || !got.Replayed {
		t.Fatal(got)
	}
}

type failedCommitJournal struct{ *store.PostgresStore }

func (failedCommitJournal) CompleteToolEffect(string, []byte) (domain.ToolEffectRecord, error) {
	return domain.ToolEffectRecord{}, os.ErrPermission
}

func TestFilesystemWriteRejectsAndSerializesConflicts(t *testing.T) {
	f := newWriteFixture(t)
	created := resultMap(t, f.execute(t, "file_write", "seed", `{"root":"output","path":"report.md","content":"Original"}`))
	for i, args := range []string{
		`{"root":"output","path":"../escape.md","content":"bad"}`,
		`{"root":"output","path":".env","content":"bad"}`,
		`{"root":"output","path":"run.sh","content":"bad"}`,
		`{"root":"output","path":"nested/report.md","content":"bad"}`,
		`{"root":"output","path":"token=unsafe-value.md","content":"safe"}`,
		`{"root":"output","path":"tab\treport.md","content":"safe"}`,
		`{"root":"output","path":"report.md","content":"bad"}`,
		`{"root":"output","path":"report.md","content":"bad","expected_hash":"` + strings.Repeat("a", 64) + `"}`,
	} {
		got := f.execute(t, "file_write", jsonNumber(i), args)
		if got.Error == nil {
			t.Fatal(got)
		}
	}
	if data, _ := os.ReadFile(filepath.Join(f.root, "report.md")); string(data) != "Original" {
		t.Fatal(string(data))
	}
	var wg sync.WaitGroup
	results := make(chan tool.ExecutionResult, 2)
	for i := range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- f.execute(t, "file_write", "concurrent-"+jsonNumber(i), `{"root":"output","path":"report.md","content":`+quoted("Updated "+jsonNumber(i))+`,"expected_hash":`+quoted(created["new_hash"].(string))+`}`)
		}()
	}
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	for result := range results {
		if result.Error == nil {
			success++
		} else if result.Error.Code == "filesystem_write_conflict" {
			conflict++
		} else {
			t.Fatal(result)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatal(success, conflict)
	}
	for _, record := range mustWriteEffects(t, f) {
		if record.Status == domain.ToolEffectNeedsReconciliation {
			t.Fatal("pre-publication rejection became uncertain", record)
		}
	}
}

func mustWriteEffects(t *testing.T, f writeFixtureState) []domain.ToolEffectRecord {
	t.Helper()
	records, err := f.store.ListToolEffects(f.run.ID)
	if err != nil {
		t.Fatal(err)
	}
	return records
}
