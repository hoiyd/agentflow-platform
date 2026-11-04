package filesystem

import (
	"context"
	"encoding/json"
	"os"
	"reflect"

	"agentflow-platform/apps/api/internal/tool"
)

type undoSession struct {
	root            rootRecord
	output, archive *os.File
	record          undoRecord
}

func (u *undoSession) close() { u.archive.Close(); u.output.Close() }

func (s *Service) openFileUndo(ctx context.Context, recovery tool.EffectReconciliationContext) (*undoSession, error) {
	root, expected, err := s.loadFileUndo(ctx, recovery)
	if err != nil {
		return nil, err
	}
	output, err := lockOutput(ctx, root)
	if err != nil {
		return nil, err
	}
	archive, err := openUndoDirectory(output, false)
	if err != nil {
		output.Close()
		return nil, failure("filesystem_undo_unavailable", "Before-image storage is missing or unsafe")
	}
	u := &undoSession{root: root, output: output, archive: archive}
	if _, _, err = sweepUndoRecords(ctx, archive, s.now()); err == nil {
		u.record, err = readUndoRecord(archive, undoName(recovery.IdempotencyKey))
	}
	if err != nil {
		u.close()
		return nil, err
	}
	r := u.record
	if r.Identity != expected.Identity || r.WorkspaceID != root.WorkspaceID || !reflect.DeepEqual(r.Receipt, expected.Receipt) || r.BeforeFileID == "" || contentHash([]byte(r.Before)) != r.Receipt.OldHash || len(r.Before) != r.Receipt.UndoBytes || validateWriteText(r.Before) != nil {
		u.close()
		return nil, failure("filesystem_undo_unavailable", "Before-image identity or integrity does not match the committed change")
	}
	if !s.now().Before(*r.Receipt.UndoExpiresAt) {
		u.close()
		return nil, failure("filesystem_undo_unavailable", "Before-image has expired")
	}
	return u, nil
}

func undoMatchesAfter(u *undoSession) error {
	current, err := inspectOutput(u.output, u.record.Receipt.Path)
	if err != nil {
		return err
	}
	if !current.exists || current.hash != u.record.Receipt.NewHash || current.identity != u.record.Receipt.FileID {
		return failure("filesystem_undo_conflict", "Report changed after this operation; current content will not be overwritten")
	}
	return nil
}

func (s *Service) previewFileUndo(ctx context.Context, recovery tool.EffectReconciliationContext) (tool.CompensationPreview, error) {
	u, err := s.openFileUndo(ctx, recovery)
	if err != nil {
		return tool.CompensationPreview{UnavailableReason: err.Error()}, nil
	}
	defer u.close()
	r := u.record.Receipt
	preview := tool.CompensationPreview{Resource: r.Root + "/" + r.Path, BeforeHash: r.OldHash, AfterHash: r.NewHash, BeforeBytes: r.UndoBytes, AfterBytes: r.Bytes, ExpiresAt: *r.UndoExpiresAt}
	if u.record.RestoreFileID != "" {
		preview.UnavailableReason = "A compensation intent already exists; verify its outcome instead of writing again"
		return preview, nil
	}
	if err := undoMatchesAfter(u); err != nil {
		preview.UnavailableReason = err.Error()
		return preview, nil
	}
	preview.Available = true
	return preview, nil
}

func (s *Service) compensateFile(ctx context.Context, recovery tool.EffectReconciliationContext) (err error) {
	intentAttempted := false
	defer func() {
		if err != nil && !intentAttempted {
			err = &tool.CompensationRejected{Err: err}
		}
	}()
	u, err := s.openFileUndo(ctx, recovery)
	if err != nil {
		return err
	}
	defer u.close()
	if u.record.RestoreFileID != "" {
		intentAttempted = true
		return verifyRestoredFile(ctx, u)
	}
	if err = undoMatchesAfter(u); err != nil {
		return err
	}
	tempName := ".agentflow-" + contentHash(randomBytes()) + ".tmp"
	temp, err := createOutputTemp(u.output, tempName)
	if err != nil {
		return failure(tool.ErrorExecutionFailed, "Cannot stage restored report")
	}
	defer temp.Close()
	defer func() { _ = removeOutputTemp(u.output, tempName) }()
	data := []byte(u.record.Before)
	if n, err := s.writes.write(temp, data); err != nil || n != len(data) {
		return failure(tool.ErrorExecutionFailed, "Restored report write failed or was incomplete")
	}
	if err = s.writes.sync(temp); err != nil {
		return failure(tool.ErrorExecutionFailed, "Restored report sync failed")
	}
	u.record.RestoreFileID, err = fileIdentity(temp)
	if err != nil {
		return failure(tool.ErrorExecutionFailed, "Restored report identity unavailable")
	}
	encoded, _ := json.Marshal(u.record)
	// From here a durable compensation intent may exist. All subsequent attempts
	// verify its exact file identity; none may reapply the before-image.
	intentAttempted = true
	if err = s.saveUndoRecord(ctx, u.archive, undoName(recovery.IdempotencyKey), encoded, false); err != nil {
		return err
	}
	if _, err = s.authorizedRootForWorkspace(u.root.WorkspaceID, u.root.ID); err != nil {
		return err
	}
	if err = s.store.CheckWorkspaceExecution(u.root.WorkspaceID); err != nil {
		return err
	}
	if err = undoMatchesAfter(u); err != nil {
		return err
	}
	if !s.now().Before(*u.record.Receipt.UndoExpiresAt) {
		return failure("filesystem_undo_unavailable", "Before-image expired before Undo publication")
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = s.writes.publish(u.output, tempName, u.record.Receipt.Path, false); err != nil {
		return failure(tool.ErrorEffectReconciliation, "Undo publication is uncertain; verify before any further action")
	}
	if err = s.writes.sync(u.output); err != nil {
		return failure(tool.ErrorEffectReconciliation, "Restored report durability is uncertain; verify its outcome")
	}
	return ctx.Err()
}

func (s *Service) verifyFileUndo(ctx context.Context, recovery tool.EffectReconciliationContext) error {
	u, err := s.openFileUndo(ctx, recovery)
	if err != nil {
		return err
	}
	defer u.close()
	return verifyRestoredFile(ctx, u)
}

func verifyRestoredFile(ctx context.Context, u *undoSession) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	current, err := inspectOutput(u.output, u.record.Receipt.Path)
	if err != nil {
		return err
	}
	if u.record.RestoreFileID == "" || !current.exists || current.hash != u.record.Receipt.OldHash || current.identity != u.record.RestoreFileID {
		return failure("filesystem_undo_unconfirmed", "Restored file identity/hash cannot be confirmed; manual disposition is required")
	}
	if err = u.output.Sync(); err != nil {
		return failure(tool.ErrorEffectReconciliation, "Restored report directory durability remains uncertain")
	}
	return ctx.Err()
}
