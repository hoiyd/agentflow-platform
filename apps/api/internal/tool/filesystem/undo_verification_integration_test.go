//go:build linux || darwin

package filesystem

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/tool/reconciliation"
)

func TestFilesystemUndoExpiryDuringStagingNeverPublishes(t *testing.T) {
	f := newUndoFixture(t)
	record := prepareUndo(t, f)
	var envelope struct {
		Result WriteResult `json:"result"`
	}
	if err := json.Unmarshal(record.Result, &envelope); err != nil {
		t.Fatal(err)
	}
	clock := envelope.Result.UndoExpiresAt.Add(-time.Second)
	f.service.now = func() time.Time { return clock }
	f.service.writes.write = func(file *os.File, data []byte) (int, error) {
		clock = envelope.Result.UndoExpiresAt.Add(time.Second)
		return file.Write(data)
	}
	result, err := reconciliation.ReconcileToolEffect(f.ctx, f.catalog, f.store, f.run, record.IdempotencyKey, undoCommand(record))
	if err != nil || result.Outcome != "failed" {
		t.Fatal(result, err)
	}
	if data, _ := os.ReadFile(filepath.Join(f.root, "report.md")); string(data) != "Updated report." {
		t.Fatal("expired data published", string(data))
	}
	// A subsequent preview/verification reclaims the expired private copy.
	command := undoCommand(record)
	command.CommandID, command.Action, command.ExpectedVersion = "verify-expired", domain.ToolEffectVerifyCompensation, result.Effect.Version
	result, err = reconciliation.ReconcileToolEffect(f.ctx, f.catalog, f.store, f.run, record.IdempotencyKey, command)
	if err != nil || result.Outcome != "failed" {
		t.Fatal(result, err)
	}
	if _, err := os.Stat(filepath.Join(f.root, undoDirectory, undoName(record.IdempotencyKey))); !os.IsNotExist(err) {
		t.Fatal("expired copy retained after sweep", err)
	}
}

func TestFilesystemUndoVerificationRefusesLaterChanges(t *testing.T) {
	for _, change := range []string{"manual-edit", "missing", "symlink"} {
		t.Run(change, func(t *testing.T) {
			f := newUndoFixture(t)
			record := prepareUndo(t, f)
			if _, err := reconciliation.ReconcileToolEffect(f.ctx, f.catalog, undoSettlementFailure{f.store}, f.run, record.IdempotencyKey, undoCommand(record)); err == nil {
				t.Fatal("lost settlement appeared successful")
			}
			path := filepath.Join(f.root, "report.md")
			switch change {
			case "manual-edit":
				if err := os.WriteFile(path, []byte("Human revision."), 0600); err != nil {
					t.Fatal(err)
				}
			case "missing":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Rename(path, path+".old"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(path+".old", path); err != nil {
					t.Fatal(err)
				}
			}
			for _, r := range mustWriteEffects(t, f) {
				if r.IdempotencyKey == record.IdempotencyKey {
					record = r
				}
			}
			command := undoCommand(record)
			command.CommandID, command.Action = "verify-later-change", domain.ToolEffectVerifyCompensation
			before, _ := os.ReadFile(path)
			result, err := reconciliation.ReconcileToolEffect(f.ctx, f.catalog, f.store, f.run, record.IdempotencyKey, command)
			if err != nil || result.Outcome != "failed" || result.Effect.Status != domain.ToolEffectReconciling {
				t.Fatal(result, err)
			}
			after, _ := os.ReadFile(path)
			if string(after) != string(before) {
				t.Fatal("verification rewrote the report")
			}
		})
	}
}

type undoReadFailure struct {
	*store.PostgresStore
	fault string
}

func (s undoReadFailure) GetRun(id string) (domain.Run, bool, error) {
	if s.fault == "run-read" {
		return domain.Run{}, false, errors.New("Run database unavailable")
	}
	return s.PostgresStore.GetRun(id)
}
func (s undoReadFailure) ListToolEffects(id string) ([]domain.ToolEffectRecord, error) {
	if s.fault == "effect-read" {
		return nil, errors.New("Effect database unavailable")
	}
	return s.PostgresStore.ListToolEffects(id)
}

func TestFilesystemUndoMissingAuthorityDataFailsClosed(t *testing.T) {
	for _, fault := range []string{"run-read", "effect-read"} {
		t.Run(fault, func(t *testing.T) {
			f := newUndoFixture(t)
			record := prepareUndo(t, f)
			f.service.store = undoReadFailure{PostgresStore: f.store, fault: fault}
			if _, err := reconciliation.ReconcileToolEffect(f.ctx, f.catalog, f.store, f.run, record.IdempotencyKey, undoCommand(record)); err == nil {
				t.Fatal("missing authority data permitted Undo")
			}
			if data, _ := os.ReadFile(filepath.Join(f.root, "report.md")); string(data) != "Updated report." {
				t.Fatal(string(data))
			}
		})
	}
}
