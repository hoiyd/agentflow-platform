//go:build linux || darwin

package filesystem

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/tool"
	"agentflow-platform/apps/api/internal/tool/policy"
	"agentflow-platform/apps/api/internal/tool/reconciliation"
)

func TestFilesystemWriteKilledWorkerRecovery(t *testing.T) {
	f := newWriteFixture(t)
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	command := exec.Command(os.Args[0], "-test.run=^TestFilesystemWriteCrashWorker$")
	command.Env = append(os.Environ(), "AGENTFLOW_WRITE_CRASH_DB="+f.database, "AGENTFLOW_WRITE_CRASH_ROOTS="+f.config, "AGENTFLOW_WRITE_CRASH_RUN="+f.run.ID)
	command.ExtraFiles = []*os.File{writer}
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	if err = command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = command.Process.Kill() }()
	writer.Close()
	if err = reader.SetReadDeadline(time.Now().Add(30 * time.Second)); err != nil {
		t.Fatal(err)
	}
	ready := []byte{0}
	if _, err = io.ReadFull(reader, ready); err != nil {
		command.Process.Kill()
		command.Wait()
		t.Fatalf("worker never reached settlement: %v %s", err, output.String())
	}
	if err = command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err = command.Wait(); err == nil {
		t.Fatal("worker was not killed")
	}
	data, err := os.ReadFile(filepath.Join(f.root, "crash.txt"))
	if err != nil || string(data) != "Crash-safe evidence" {
		t.Fatal(string(data), err)
	}
	record := mustWriteEffects(t, f)[0]
	if record.Status != domain.ToolEffectExecuting || len(record.Result) == 0 {
		t.Fatal(record)
	}
	// The existing orphan-repair boundary moves executing receipts to uncertain;
	// this fixture invokes that Store operation after an actual worker kill.
	record, err = f.store.MarkToolEffectNeedsReconciliation(record.IdempotencyKey, "worker terminated before journal settlement")
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := New(f.config, f.store)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := tool.NewCatalogWithPolicy(f.catalog.SecurityPolicy(), restarted.Bindings()...)
	if err != nil {
		t.Fatal(err)
	}
	reconcile := reconciliation.ToolEffectReconciliationCommand{CommandID: "after-kill", Action: domain.ToolEffectRetrySameKey, ExpectedVersion: record.Version, Actor: "operator", Reason: "Verify published file identity after worker crash"}
	outcome, err := reconciliation.ReconcileToolEffect(f.ctx, catalog, f.store, f.run, record.IdempotencyKey, reconcile)
	if err != nil || outcome.Effect.Status != domain.ToolEffectCommitted {
		t.Fatal(outcome, err)
	}
	if _, err = reconciliation.ReconcileToolEffect(f.ctx, catalog, f.store, f.run, record.IdempotencyKey, reconcile); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(f.root, "crash.txt"))
	if !bytes.Equal(data, after) {
		t.Fatal("recovery rewrote report")
	}
}

type crashSettlementJournal struct{ *store.PostgresStore }

func (crashSettlementJournal) CompleteToolEffect(string, []byte) (domain.ToolEffectRecord, error) {
	ready := os.NewFile(3, "publication-ready")
	_, _ = ready.Write([]byte{1})
	_ = ready.Close()
	select {}
}

func TestFilesystemWriteCrashWorker(t *testing.T) {
	database := os.Getenv("AGENTFLOW_WRITE_CRASH_DB")
	if database == "" {
		t.Skip("subprocess-only fault worker")
	}
	storage, err := store.NewPostgresStore(database)
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Close()
	run, found, err := storage.GetRun(os.Getenv("AGENTFLOW_WRITE_CRASH_RUN"))
	if err != nil || !found {
		t.Fatal(err)
	}
	service, err := New(os.Getenv("AGENTFLOW_WRITE_CRASH_ROOTS"), storage)
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
	ctx := event.WithScope(t.Context(), event.Scope{RunID: run.ID, ConversationID: run.ConversationID, TurnID: "write-turn"})
	executor := tool.NewExecutor(catalog, tool.ExecutorOptions{EffectJournal: crashSettlementJournal{storage}, Tracer: event.NewToolExecutionTracer(event.NewRecorder(storage), run.ID, "")})
	executor.Execute(ctx, tool.ExecutionRequest{RunID: run.ID, TurnID: "write-turn", CallID: "killed", Tool: "file_write", Arguments: json.RawMessage(`{"root":"output","path":"crash.txt","content":"Crash-safe evidence"}`)})
	t.Fatal("crash worker unexpectedly settled")
}
