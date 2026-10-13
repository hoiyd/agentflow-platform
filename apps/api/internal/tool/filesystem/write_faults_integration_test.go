//go:build linux || darwin

package filesystem

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/tool"
	"agentflow-platform/apps/api/internal/tool/reconciliation"
	"golang.org/x/sys/unix"
)

// Only OS failure edges are injected. Real Descriptor, Handler, target files,
// Executor, journal and audited reconciliation stay in the path.
func TestFilesystemWriteFaultWindowsPostgres(t *testing.T) {
	for _, phase := range []string{"short write", "disk full", "temp sync", "publish", "publication ack lost", "directory sync", "cancel before publish", "cancel after publish"} {
		t.Run(phase, func(t *testing.T) {
			f := newWriteFixture(t)
			ctx, cancel := context.WithCancel(f.ctx)
			defer cancel()
			f.ctx = ctx
			switch phase {
			case "short write":
				f.service.writes.write = func(*os.File, []byte) (int, error) { return 1, nil }
			case "disk full":
				f.service.writes.write = func(*os.File, []byte) (int, error) { return 0, unix.ENOSPC }
			case "temp sync":
				f.service.writes.sync = func(*os.File) error { return unix.ENOSPC }
			case "publish":
				f.service.writes.publish = func(*os.File, string, string, bool) error { return unix.EIO }
			case "publication ack lost":
				f.service.writes.publish = func(dir *os.File, from, to string, create bool) error {
					if err := publishOutput(dir, from, to, create); err != nil {
						return err
					}
					return unix.EIO
				}
			case "directory sync":
				f.service.writes.sync = func(file *os.File) error {
					if info, _ := file.Stat(); info.IsDir() {
						return unix.EIO
					}
					return file.Sync()
				}
			case "cancel before publish":
				f.service.writes.sync = func(file *os.File) error { err := file.Sync(); cancel(); return err }
			case "cancel after publish":
				f.service.writes.publish = func(dir *os.File, from, to string, create bool) error {
					err := publishOutput(dir, from, to, create)
					cancel()
					return err
				}
			}
			got := f.execute(t, "file_write", "fault", `{"root":"output","path":"report.md","content":"Durable report"}`)
			if got.Error == nil {
				t.Fatal("fault certified as successful", phase)
			}
			published := phase == "directory sync" || phase == "cancel after publish" || phase == "publication ack lost"
			// Executor may return cancellation before its Handler completes. Taking
			// the same lock is a deterministic cleanup barrier, not a timing sleep.
			root := f.service.roots[0]
			dir, err := lockOutput(t.Context(), root)
			if err != nil {
				t.Fatal(err)
			}
			dir.Close()
			data, err := os.ReadFile(filepath.Join(f.root, "report.md"))
			if published {
				if err != nil || string(data) != "Durable report" {
					t.Fatal(string(data), err)
				}
			} else if !errors.Is(err, os.ErrNotExist) {
				t.Fatal("partial report published", string(data), err)
			}
			entries, err := os.ReadDir(f.root)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), ".agentflow-") {
					t.Fatal("staging file leaked", entry.Name())
				}
			}
			records := mustWriteEffects(t, f)
			if len(records) != 1 {
				t.Fatal(records)
			}
			if published {
				if records[0].Status != domain.ToolEffectNeedsReconciliation {
					t.Fatal(records)
				}
			} else if records[0].Status != domain.ToolEffectFailed && records[0].Status != domain.ToolEffectNeedsReconciliation {
				t.Fatal(records)
			}
			if published {
				command := reconciliation.ToolEffectReconciliationCommand{CommandID: "inspect", Action: domain.ToolEffectRetrySameKey, ExpectedVersion: records[0].Version, Actor: "operator", Reason: "Check target without repeating write"}
				outcome, err := reconciliation.ReconcileToolEffect(t.Context(), f.catalog, f.store, f.run, records[0].IdempotencyKey, command)
				if err != nil || outcome.Effect.Status != domain.ToolEffectCommitted {
					t.Fatal(outcome, err)
				}
			}
		})
	}
}

func TestFilesystemWriteUnsafeTargetsAndCorrection(t *testing.T) {
	f := newWriteFixture(t)
	outside := filepath.Join(t.TempDir(), "outside.txt")
	writeFixture(t, outside, "UNCHANGED")
	if err := os.Symlink(outside, filepath.Join(f.root, "symlink.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(outside, filepath.Join(f.root, "hardlink.txt")); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mkfifo(filepath.Join(f.root, "fifo.txt"), 0600); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(f.root, "exec.md"), "Executable")
	if err := os.Chmod(filepath.Join(f.root, "exec.md"), 0700); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(f.root, "large.txt"), strings.Repeat("x", maxWriteBytes+1))
	for _, path := range []string{"symlink.txt", "hardlink.txt", "fifo.txt", "exec.md", "large.txt"} {
		if got := f.execute(t, "file_write", path, `{"root":"output","path":`+quoted(path)+`,"content":"Overwrite","expected_hash":`+quoted(contentHash([]byte("UNCHANGED")))+`}`); got.Error == nil {
			t.Fatal(got)
		}
	}
	if data, _ := os.ReadFile(outside); string(data) != "UNCHANGED" {
		t.Fatal("link target changed")
	}
	for i, content := range []string{"secret: token=unsafe-value", "a\x00b", strings.Repeat("x", maxWriteBytes+1), strings.Repeat("中", maxWriteBytes/2)} {
		if got := f.execute(t, "file_write", "content-"+jsonNumber(i), `{"root":"output","path":"rejected.txt","content":`+quoted(content)+`}`); got.Error == nil {
			t.Fatal(got)
		}
	}
	for i, content := range []string{"", "中文 evidence"} {
		name := "valid-" + jsonNumber(i) + ".md"
		resultMap(t, f.execute(t, "file_write", name, `{"root":"output","path":`+quoted(name)+`,"content":`+quoted(content)+`}`))
	}
	seed := resultMap(t, f.execute(t, "file_write", "seed", `{"root":"output","path":"patch.md","content":"same same"}`))
	for i, old := range []string{"missing", "same"} {
		if got := f.execute(t, "file_patch", "ambiguous-"+jsonNumber(i), `{"root":"output","path":"patch.md","expected_hash":`+quoted(seed["new_hash"].(string))+`,"old_text":`+quoted(old)+`,"new_text":"new"}`); got.Error == nil || got.Error.Code != "filesystem_write_conflict" {
			t.Fatal(got)
		}
	}
	resultMap(t, f.execute(t, "file_patch", "corrected", `{"root":"output","path":"patch.md","expected_hash":`+quoted(seed["new_hash"].(string))+`,"old_text":"same same","new_text":"corrected"}`))
	for _, record := range mustWriteEffects(t, f) {
		if record.Status == domain.ToolEffectNeedsReconciliation {
			t.Fatal(record)
		}
	}
}

func TestFilesystemWriteRecoveryRejectsChangedTarget(t *testing.T) {
	f := newWriteFixture(t)
	executor := tool.NewExecutor(f.catalog, tool.ExecutorOptions{EffectJournal: failedCommitJournal{f.store}, Tracer: event.NewToolExecutionTracer(event.NewRecorder(f.store), f.run.ID, "")})
	request := tool.ExecutionRequest{RunID: f.run.ID, TurnID: "write-turn", CallID: "pending", Tool: "file_write", Arguments: json.RawMessage(`{"root":"output","path":"report.txt","content":"Evidence"}`)}
	if got := executor.Execute(f.ctx, request); got.Error == nil {
		t.Fatal(got)
	}
	record := mustWriteEffects(t, f)[0]
	writeFixture(t, filepath.Join(f.root, "report.txt"), "Changed by operator")
	command := reconciliation.ToolEffectReconciliationCommand{CommandID: "changed", Action: domain.ToolEffectRetrySameKey, ExpectedVersion: record.Version, Actor: "operator", Reason: "Confirm publication"}
	outcome, err := reconciliation.ReconcileToolEffect(f.ctx, f.catalog, f.store, f.run, record.IdempotencyKey, command)
	if err != nil || outcome.Effect.Status != domain.ToolEffectNeedsReconciliation {
		t.Fatal(outcome, err)
	}
	current := mustWriteEffects(t, f)[0]
	if !strings.Contains(string(current.Result), "new_hash") {
		t.Fatal("failed recovery discarded intent", current)
	}
	if data, _ := os.ReadFile(filepath.Join(f.root, "report.txt")); string(data) != "Changed by operator" {
		t.Fatal("recovery rewrote changed report")
	}
	if got := f.execute(t, "file_write", "pending", string(request.Arguments)); got.Error == nil || got.Error.Code != tool.ErrorEffectReconciliation {
		t.Fatal(got)
	}
	if err := os.WriteFile(f.config, []byte(`{"roots":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	command.CommandID = "revoked"
	command.ExpectedVersion = current.Version
	outcome, err = reconciliation.ReconcileToolEffect(f.ctx, f.catalog, f.store, f.run, record.IdempotencyKey, command)
	if err == nil && outcome.Effect.Status == domain.ToolEffectCommitted {
		t.Fatal("revoked root recovered", outcome)
	}
}

func TestFilesystemWriteCreateRaceCannotFollowOrOverwrite(t *testing.T) {
	f := newWriteFixture(t)
	outside := filepath.Join(t.TempDir(), "outside.txt")
	writeFixture(t, outside, "UNCHANGED")
	f.service.writes.publish = func(dir *os.File, from, to string, create bool) error {
		if err := os.Symlink(outside, filepath.Join(f.root, to)); err != nil {
			return err
		}
		return publishOutput(dir, from, to, create)
	}
	result := f.execute(t, "file_write", "create-race", `{"root":"output","path":"report.md","content":"New report"}`)
	if result.Error == nil || result.Error.Code != "filesystem_write_conflict" || !result.EffectNotApplied {
		t.Fatal(result)
	}
	if data, _ := os.ReadFile(outside); string(data) != "UNCHANGED" {
		t.Fatal("symlink target changed")
	}
	if info, err := os.Lstat(filepath.Join(f.root, "report.md")); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("existing target was overwritten", err)
	}
	if records := mustWriteEffects(t, f); records[0].Status != domain.ToolEffectFailed {
		t.Fatal(records)
	}
}
