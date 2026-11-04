//go:build linux || darwin

package filesystem

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/identity"
	"agentflow-platform/apps/api/internal/tool"
	"agentflow-platform/apps/api/internal/tool/policy"
	"agentflow-platform/apps/api/internal/tool/reconciliation"
)

func newUndoFixture(t *testing.T) writeFixtureState {
	t.Helper()
	f := newWriteFixture(t)
	return configureUndoFixture(t, f, 1048576)
}

func configureUndoFixture(t *testing.T, f writeFixtureState, budget int) writeFixtureState {
	t.Helper()
	writeFixture(t, f.config, `{"roots":[{"id":"output","workspace_id":`+quoted(f.run.WorkspaceID)+`,"path":`+quoted(f.root)+`,"write_access":"managed_output","undo_retention_seconds":3600,"undo_max_bytes":`+jsonNumber(budget)+`}]}`)
	var err error
	f.service, err = New(f.config, f.store)
	if err != nil {
		t.Fatal(err)
	}
	rules := []policy.Rule{}
	for _, b := range f.service.Bindings() {
		rules = append(rules, policy.Rule{ID: b.Descriptor.Name, Tool: b.Descriptor.Name, Action: policy.ActionAllowAndLog, Capability: b.Descriptor.Security})
	}
	f.catalog, err = tool.NewCatalogWithPolicy(policy.Policy{Version: "undo-fixture", DefaultAction: policy.ActionDeny, Rules: rules}, f.service.Bindings()...)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func prepareUndo(t *testing.T, f writeFixtureState) domain.ToolEffectRecord {
	t.Helper()
	created := resultMap(t, f.execute(t, "file_write", "create", `{"root":"output","path":"report.md","content":"Original report."}`))
	resultMap(t, f.execute(t, "file_patch", "patch", `{"root":"output","path":"report.md","expected_hash":`+quoted(created["new_hash"].(string))+`,"old_text":"Original","new_text":"Updated"}`))
	if _, err := f.store.UpdateRunStatus(f.run.ID, domain.RunCompleted, ""); err != nil {
		t.Fatal(err)
	}
	for _, record := range mustWriteEffects(t, f) {
		if record.ToolCallID == "patch" {
			return record
		}
	}
	t.Fatal("missing patch receipt")
	return domain.ToolEffectRecord{}
}

func undoCommand(record domain.ToolEffectRecord) reconciliation.ToolEffectReconciliationCommand {
	return reconciliation.ToolEffectReconciliationCommand{CommandID: "undo-report", Action: domain.ToolEffectCompensate, ExpectedVersion: record.Version, Actor: "fixture-owner", Reason: "Restore the previous report"}
}

func TestFilesystemUndoRestoresReportAndReplaysCommand(t *testing.T) {
	f := newUndoFixture(t)
	record := prepareUndo(t, f)
	if !strings.Contains(string(record.Result), "undo_expires_at") || strings.Contains(string(record.Result), "Original report.") {
		t.Fatalf("receipt must expose bounded metadata, never before text: %s", record.Result)
	}
	command := undoCommand(record)
	outcome, err := reconciliation.ReconcileToolEffect(f.ctx, f.catalog, f.store, f.run, record.IdempotencyKey, command)
	if err != nil || outcome.Outcome != "completed" || outcome.Effect.Status != domain.ToolEffectCompensated {
		t.Fatal(outcome, err)
	}
	data, err := os.ReadFile(filepath.Join(f.root, "report.md"))
	if err != nil || string(data) != "Original report." {
		t.Fatal(string(data), err)
	}
	restarted, err := New(f.config, f.store)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := tool.NewCatalogWithPolicy(f.catalog.SecurityPolicy(), restarted.Bindings()...)
	if err != nil {
		t.Fatal(err)
	}
	again, err := reconciliation.ReconcileToolEffect(f.ctx, catalog, f.store, f.run, record.IdempotencyKey, command)
	if err != nil || again.Applied || again.Effect.Status != domain.ToolEffectCompensated {
		t.Fatal(again, err)
	}
	read := resultMap(t, f.execute(t, "file_read", "verify", `{"root":"output","path":"report.md"}`))
	if read["content"] != "Original report." {
		t.Fatal(read)
	}
	for _, event := range mustUndoEvents(t, f) {
		encoded, _ := json.Marshal(event)
		if strings.Contains(string(encoded), "Original report.") && strings.Contains(string(encoded), "compensation") {
			t.Fatal("before text in audit")
		}
	}
}

func TestFilesystemUndoRejectsConflictsWithoutChangingCommittedEffect(t *testing.T) {
	for _, mutation := range []string{"human-edit", "other-run-write", "same-bytes-new-inode", "symlink", "root-revoked", "missing-before-image", "expired", "corrupt", "tampered-content", "tampered-owner", "oversized-image", "unexpected-entry", "archive-symlink", "archive-hardlink", "archive-permissions", "revoked-tool", "stale-version", "active-run", "archived-workspace", "create"} {
		t.Run(mutation, func(t *testing.T) {
			f := newUndoFixture(t)
			record := prepareUndo(t, f)
			path := filepath.Join(f.root, "report.md")
			switch mutation {
			case "human-edit":
				if err := os.WriteFile(path, []byte("Human revision."), 0600); err != nil {
					t.Fatal(err)
				}
			case "other-run-write":
				other := f
				var err error
				other.run, err = f.store.CreateRunWithContract(f.run.AgentID, f.run.ConversationID, domain.RuntimeSnapshot{SchemaVersion: domain.CurrentRuntimeSnapshotVersion, RunBudget: &domain.RuntimeRunBudget{}}, nil)
				if err != nil {
					t.Fatal(err)
				}
				other.ctx = event.WithScope(t.Context(), event.Scope{RunID: other.run.ID, ConversationID: other.run.ConversationID, TurnID: "write-turn"})
				resultMap(t, other.execute(t, "file_write", "later", `{"root":"output","path":"report.md","content":"Later revision.","expected_hash":`+quoted(contentHash([]byte("Updated report.")))+`}`))
			case "same-bytes-new-inode":
				if err := os.Rename(path, path+".old"); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("Updated report."), 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Rename(path, path+".old"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(path+".old", path); err != nil {
					t.Fatal(err)
				}
			case "root-revoked":
				writeFixture(t, f.config, `{"roots":[]}`)
			case "missing-before-image":
				if err := os.RemoveAll(filepath.Join(f.root, ".agentflow-undo")); err != nil {
					t.Fatal(err)
				}
			case "create":
				for _, r := range mustWriteEffects(t, f) {
					if r.ToolCallID == "create" {
						record = r
					}
				}
			case "expired":
				name := filepath.Join(f.root, undoDirectory, undoName(record.IdempotencyKey))
				data, err := os.ReadFile(name)
				if err != nil {
					t.Fatal(err)
				}
				var image undoRecord
				if err = json.Unmarshal(data, &image); err != nil {
					t.Fatal(err)
				}
				expired := time.Now().Add(-time.Hour)
				image.Receipt.UndoExpiresAt = &expired
				data, _ = json.Marshal(image)
				if err = os.WriteFile(name, data, 0600); err != nil {
					t.Fatal(err)
				}
			case "corrupt":
				if err := os.WriteFile(filepath.Join(f.root, undoDirectory, undoName(record.IdempotencyKey)), []byte(`{"corrupt":true}`), 0600); err != nil {
					t.Fatal(err)
				}
			case "tampered-content", "tampered-owner":
				name := filepath.Join(f.root, undoDirectory, undoName(record.IdempotencyKey))
				data, err := os.ReadFile(name)
				if err != nil {
					t.Fatal(err)
				}
				var image undoRecord
				if err = json.Unmarshal(data, &image); err != nil {
					t.Fatal(err)
				}
				if mutation == "tampered-owner" {
					image.WorkspaceID = "999999"
				} else {
					image.Before = "Unrelated report"
				}
				data, _ = json.Marshal(image)
				if err = os.WriteFile(name, data, 0600); err != nil {
					t.Fatal(err)
				}
			case "oversized-image":
				if err := os.WriteFile(filepath.Join(f.root, undoDirectory, undoName(record.IdempotencyKey)), []byte(strings.Repeat("x", maxUndoRecordBytes+1)), 0600); err != nil {
					t.Fatal(err)
				}
			case "unexpected-entry":
				if err := os.WriteFile(filepath.Join(f.root, undoDirectory, "orphan.tmp"), []byte("interrupted staging"), 0600); err != nil {
					t.Fatal(err)
				}
			case "archive-permissions":
				if err := os.Chmod(filepath.Join(f.root, undoDirectory), 0755); err != nil {
					t.Fatal(err)
				}
			case "archive-symlink":
				dir := filepath.Join(f.root, undoDirectory)
				if err := os.Rename(dir, dir+"-old"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(dir+"-old", dir); err != nil {
					t.Fatal(err)
				}
			case "archive-hardlink":
				if err := os.Link(filepath.Join(f.root, undoDirectory, undoName(record.IdempotencyKey)), filepath.Join(t.TempDir(), "alias")); err != nil {
					t.Fatal(err)
				}
			case "revoked-tool":
				if err := f.catalog.SetEnabled("file_patch", false); err != nil {
					t.Fatal(err)
				}
			case "stale-version":
				record.Version++
			case "active-run":
				if _, err := f.store.UpdateRunStatus(f.run.ID, domain.RunRunning, ""); err != nil {
					t.Fatal(err)
				}
			case "archived-workspace":
				replacement, err := f.store.CreateWorkspace(t.Context(), identity.SuperUserID, "Replacement workspace", "")
				if err != nil {
					t.Fatal(err)
				}
				status := "archived"
				if _, err := f.store.UpdateWorkspace(t.Context(), identity.SuperUserID, f.run.WorkspaceID, domain.WorkspaceUpdate{Status: &status, ReplacementWorkspaceID: replacement.ID}, false); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := os.ReadFile(path)
			outcome, err := reconciliation.ReconcileToolEffect(f.ctx, f.catalog, f.store, f.run, record.IdempotencyKey, undoCommand(record))
			if err == nil && outcome.Outcome != "failed" {
				t.Fatal("unsafe undo succeeded", outcome)
			}
			after, _ := os.ReadFile(path)
			if string(before) != string(after) {
				t.Fatal("conflict overwrote current content")
			}
			for _, r := range mustWriteEffects(t, f) {
				if r.IdempotencyKey == record.IdempotencyKey && r.Status != domain.ToolEffectCommitted {
					t.Fatal("pre-write conflict damaged original receipt", r)
				}
			}
		})
	}
}

func mustUndoEvents(t *testing.T, f writeFixtureState) []domain.RunEvent {
	t.Helper()
	events, err := f.store.ListRunEvents(f.run.ID)
	if err != nil {
		t.Fatal(err)
	}
	return events
}

func TestFilesystemUndoVerifiesUncertainCompensationWithoutWritingAgain(t *testing.T) {
	f := newUndoFixture(t)
	record := prepareUndo(t, f)
	f.service.writes.sync = func(file *os.File) error {
		if file.Name() == "." {
			return errors.New("directory sync failed after publication")
		}
		return file.Sync()
	}
	outcome, err := reconciliation.ReconcileToolEffect(f.ctx, f.catalog, f.store, f.run, record.IdempotencyKey, undoCommand(record))
	if err != nil || outcome.Outcome != "failed" {
		t.Fatal(outcome, err)
	}
	if data, _ := os.ReadFile(filepath.Join(f.root, "report.md")); string(data) != "Original report." {
		t.Fatal(string(data))
	}
	restarted, err := New(f.config, f.store)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := tool.NewCatalogWithPolicy(f.catalog.SecurityPolicy(), restarted.Bindings()...)
	if err != nil {
		t.Fatal(err)
	}
	command := undoCommand(record)
	command.CommandID, command.Action, command.ExpectedVersion = "verify-undo", domain.ToolEffectReconciliationAction("verify_compensation"), outcome.Effect.Version
	verified, err := reconciliation.ReconcileToolEffect(f.ctx, catalog, f.store, f.run, record.IdempotencyKey, command)
	if err != nil || verified.Outcome != "completed" || verified.Effect.Status != domain.ToolEffectCompensated {
		t.Fatal(verified, err)
	}
	if data, _ := os.ReadFile(filepath.Join(f.root, "report.md")); string(data) != "Original report." {
		t.Fatal(string(data))
	}
}
