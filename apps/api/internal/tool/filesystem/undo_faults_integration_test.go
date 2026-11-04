//go:build linux || darwin

package filesystem

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/tool/reconciliation"
)

func TestFilesystemUndoRetentionFailurePreventsOriginalPublication(t *testing.T) {
	for _, fault := range []string{"budget", "snapshot-write", "snapshot-sync", "snapshot-publish", "snapshot-directory-sync"} {
		t.Run(fault, func(t *testing.T) {
			f := newUndoFixture(t)
			if fault == "budget" {
				f = configureUndoFixture(t, f, 1)
			}
			created := resultMap(t, f.execute(t, "file_write", "create", `{"root":"output","path":"report.md","content":"Original"}`))
			writes, syncs := 0, 0
			f.service.writes.write = func(file *os.File, data []byte) (int, error) {
				writes++
				if fault == "snapshot-write" && writes == 2 {
					return 0, errors.New("disk full")
				}
				return file.Write(data)
			}
			f.service.writes.sync = func(file *os.File) error {
				syncs++
				if fault == "snapshot-sync" && syncs == 2 || fault == "snapshot-directory-sync" && file.Name() == undoDirectory {
					return errors.New("disk sync failed")
				}
				return file.Sync()
			}
			f.service.writes.publish = func(dir *os.File, from, to string, only bool) error {
				if fault == "snapshot-publish" && dir.Name() == undoDirectory {
					return errors.New("disk full")
				}
				return publishOutput(dir, from, to, only)
			}
			got := f.execute(t, "file_write", "replace", `{"root":"output","path":"report.md","content":"Replacement","expected_hash":`+quoted(created["new_hash"].(string))+`}`)
			if got.Error == nil {
				t.Fatal("publication bypassed retention failure")
			}
			if data, _ := os.ReadFile(filepath.Join(f.root, "report.md")); string(data) != "Original" {
				t.Fatal(string(data))
			}
			for _, record := range mustWriteEffects(t, f) {
				if record.ToolCallID == "replace" && record.Status != domain.ToolEffectFailed {
					t.Fatal(record)
				}
			}
		})
	}
}

func TestFilesystemUndoFaultWindowsPreserveBytes(t *testing.T) {
	for _, fault := range []string{"stage-short-write", "stage-sync", "intent-write", "intent-publish", "cancel", "root-drift", "publication"} {
		t.Run(fault, func(t *testing.T) {
			f := newUndoFixture(t)
			record := prepareUndo(t, f)
			ctx, cancel := context.WithCancel(f.ctx)
			defer cancel()
			writes := 0
			f.service.writes.write = func(file *os.File, data []byte) (int, error) {
				writes++
				if fault == "stage-short-write" && writes == 1 {
					return 0, nil
				}
				if fault == "intent-write" && writes == 2 {
					return 0, errors.New("disk full")
				}
				if fault == "cancel" {
					cancel()
				}
				if fault == "root-drift" {
					writeFixture(t, f.config, `{"roots":[]}`)
				}
				return file.Write(data)
			}
			f.service.writes.sync = func(file *os.File) error {
				if fault == "stage-sync" {
					return errors.New("disk full")
				}
				return file.Sync()
			}
			f.service.writes.publish = func(dir *os.File, from, to string, only bool) error {
				if fault == "intent-publish" && dir.Name() == undoDirectory || fault == "publication" && dir.Name() == "." {
					return errors.New("publication failed")
				}
				return publishOutput(dir, from, to, only)
			}
			outcome, err := reconciliation.ReconcileToolEffect(ctx, f.catalog, f.store, f.run, record.IdempotencyKey, undoCommand(record))
			if err == nil && outcome.Outcome != "failed" {
				t.Fatal(outcome)
			}
			if data, _ := os.ReadFile(filepath.Join(f.root, "report.md")); string(data) != "Updated report." {
				t.Fatal(string(data))
			}
			if fault == "stage-short-write" || fault == "stage-sync" {
				for _, r := range mustWriteEffects(t, f) {
					if r.IdempotencyKey == record.IdempotencyKey && r.Status != domain.ToolEffectCommitted {
						t.Fatal("confirmed rejection lost committed receipt", r)
					}
				}
			}
		})
	}
}

type undoSettlementFailure struct{ *store.PostgresStore }

func (s undoSettlementFailure) CommitToolEffectReconciliation(m domain.ToolEffectReconciliation) (domain.ToolEffectRecord, domain.RunEvent, bool, error) {
	if m.Event.Type == domain.EventToolEffectReconciled {
		return domain.ToolEffectRecord{}, domain.RunEvent{}, false, errors.New("event settlement unavailable")
	}
	return s.PostgresStore.CommitToolEffectReconciliation(m)
}

func TestFilesystemUndoSettlementFailureAndConcurrentClaims(t *testing.T) {
	t.Run("settlement", func(t *testing.T) {
		f := newUndoFixture(t)
		record := prepareUndo(t, f)
		if _, err := reconciliation.ReconcileToolEffect(f.ctx, f.catalog, undoSettlementFailure{f.store}, f.run, record.IdempotencyKey, undoCommand(record)); err == nil {
			t.Fatal("lost settlement reported success")
		}
		if data, _ := os.ReadFile(filepath.Join(f.root, "report.md")); string(data) != "Original report." {
			t.Fatal(string(data))
		}
		for _, r := range mustWriteEffects(t, f) {
			if r.IdempotencyKey == record.IdempotencyKey {
				record = r
			}
		}
		if record.Status != domain.ToolEffectReconciling {
			t.Fatal(record)
		}
		command := undoCommand(record)
		command.CommandID, command.Action = "verify-settlement", domain.ToolEffectVerifyCompensation
		result, err := reconciliation.ReconcileToolEffect(f.ctx, f.catalog, f.store, f.run, record.IdempotencyKey, command)
		if err != nil || result.Effect.Status != domain.ToolEffectCompensated {
			t.Fatal(result, err)
		}
	})
	t.Run("concurrent", func(t *testing.T) {
		f := newUndoFixture(t)
		record := prepareUndo(t, f)
		var publications atomic.Int32
		f.service.writes.publish = func(dir *os.File, from, to string, only bool) error {
			if dir.Name() == "." {
				publications.Add(1)
			}
			return publishOutput(dir, from, to, only)
		}
		var wg sync.WaitGroup
		for i := range 2 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				command := undoCommand(record)
				command.CommandID += jsonNumber(i)
				_, _ = reconciliation.ReconcileToolEffect(f.ctx, f.catalog, f.store, f.run, record.IdempotencyKey, command)
			}()
		}
		wg.Wait()
		if publications.Load() != 1 {
			t.Fatal(publications.Load())
		}
		if data, _ := os.ReadFile(filepath.Join(f.root, "report.md")); string(data) != "Original report." {
			t.Fatal(string(data))
		}
	})
}

func TestFilesystemUndoRetentionIsOptInAndPrivate(t *testing.T) {
	f := newWriteFixture(t)
	record := prepareUndo(t, f)
	if _, err := os.Stat(filepath.Join(f.root, undoDirectory)); !os.IsNotExist(err) {
		t.Fatal("default created before-image storage", err)
	}
	if _, err := reconciliation.ReconcileToolEffect(f.ctx, f.catalog, f.store, f.run, record.IdempotencyKey, undoCommand(record)); err == nil {
		t.Fatal("Undo acquired implicit authority")
	}
	f = newUndoFixture(t)
	prepareUndo(t, f)
	for _, toolName := range []string{"file_read", "file_list", "file_search"} {
		if result := f.execute(t, toolName, "private-"+toolName, `{"root":"output","path":".agentflow-undo","query":"Original"}`); result.Error == nil {
			t.Fatal("private before-image exposed by", toolName)
		}
	}
}
