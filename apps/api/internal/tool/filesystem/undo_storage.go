package filesystem

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"regexp"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/tool"
)

const undoDirectory = ".agentflow-undo"
const maxUndoRecordBytes = 2 * maxWriteBytes

var undoRecordName = regexp.MustCompile(`^[a-f0-9]{64}\.json$`)

// The retained copy stays under the private output root; receipts and previews
// contain metadata only. The original expiry never extends.
type undoRecord struct {
	Identity      tool.EffectIdentity `json:"identity"`
	WorkspaceID   string              `json:"workspace_id"`
	Receipt       WriteResult         `json:"receipt"`
	Before        string              `json:"before"`
	BeforeFileID  string              `json:"before_file_id"`
	RestoreFileID string              `json:"restore_file_id,omitempty"`
}

func undoName(key string) string { return contentHash([]byte(key)) + ".json" }

func readUndoRecord(dir *os.File, name string) (undoRecord, error) {
	file, err := openPrivateEntry(dir, name)
	if err != nil {
		return undoRecord{}, failure("filesystem_undo_unavailable", "Retained before-image is missing or unsafe")
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxUndoRecordBytes+1))
	var record undoRecord
	if err != nil || len(data) > maxUndoRecordBytes || json.Unmarshal(data, &record) != nil || record.Receipt.UndoExpiresAt == nil {
		return undoRecord{}, failure("filesystem_undo_unavailable", "Retained before-image is invalid")
	}
	return record, nil
}

// Reclamation is lazy on writes/previews/Undo, under the same root lock.
// No expired image is usable even when an inactive root has not been swept.
func sweepUndoRecords(ctx context.Context, dir *os.File, now time.Time) (int64, int, error) {
	entries, err := dir.ReadDir(-1)
	if err != nil {
		return 0, 0, failure(tool.ErrorExecutionFailed, "Cannot inspect Undo retention budget")
	}
	var bytes int64
	count := 0
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return 0, 0, err
		}
		if !undoRecordName.MatchString(entry.Name()) {
			return 0, 0, failure("filesystem_undo_unavailable", "Unexpected entry in private Undo storage; operator cleanup required")
		}
		record, err := readUndoRecord(dir, entry.Name())
		if err != nil {
			return 0, 0, err
		}
		if !now.Before(*record.Receipt.UndoExpiresAt) {
			if err = removeOutputTemp(dir, entry.Name()); err != nil {
				return 0, 0, failure(tool.ErrorExecutionFailed, "Cannot purge expired before-image")
			}
			continue
		}
		file, err := openPrivateEntry(dir, entry.Name())
		if err != nil {
			return 0, 0, failure(tool.ErrorExecutionFailed, "Cannot inspect retained image size")
		}
		info, err := file.Stat()
		file.Close()
		if err != nil {
			return 0, 0, failure(tool.ErrorExecutionFailed, "Cannot inspect retained image size")
		}
		bytes += info.Size()
		count++
	}
	if err = dir.Sync(); err != nil {
		return 0, 0, failure(tool.ErrorExecutionFailed, "Cannot sync Undo retention cleanup")
	}
	return bytes, count, nil
}

func (s *Service) retainBeforeImage(ctx context.Context, output *os.File, root rootRecord, identity tool.EffectIdentity, receipt WriteResult, before outputState) error {
	dir, err := openUndoDirectory(output, true)
	if err != nil {
		return failure(tool.ErrorExecutionFailed, "Cannot open private Undo storage")
	}
	defer dir.Close()
	used, count, err := sweepUndoRecords(ctx, dir, s.now())
	if err != nil {
		return err
	}
	record := undoRecord{Identity: identity, WorkspaceID: root.WorkspaceID, Receipt: receipt, Before: before.content, BeforeFileID: before.identity}
	data, err := json.Marshal(record)
	if err != nil || len(data) > maxUndoRecordBytes || int64(len(data))+used+256 > root.UndoMaxBytes || count >= 256 {
		return failure("filesystem_undo_limit", "Before-image retention budget is full; expire or discard old images before writing")
	}
	return s.saveUndoRecord(ctx, dir, undoName(identity.Key), data, true)
}

func (s *Service) saveUndoRecord(ctx context.Context, dir *os.File, name string, data []byte, createOnly bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	tempName := ".agentflow-" + contentHash(randomBytes()) + ".tmp"
	temp, err := createOutputTemp(dir, tempName)
	if err != nil {
		return failure(tool.ErrorExecutionFailed, "Cannot stage before-image")
	}
	defer temp.Close()
	defer func() { _ = removeOutputTemp(dir, tempName) }()
	if n, err := s.writes.write(temp, data); err != nil || n != len(data) {
		return failure(tool.ErrorExecutionFailed, "Before-image write failed or was incomplete")
	}
	if err = s.writes.sync(temp); err != nil {
		return failure(tool.ErrorExecutionFailed, "Before-image sync failed")
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = s.writes.publish(dir, tempName, name, createOnly); err != nil {
		return failure(tool.ErrorExecutionFailed, "Cannot publish before-image evidence")
	}
	if err = s.writes.sync(dir); err != nil {
		return failure(tool.ErrorExecutionFailed, "Before-image directory sync failed")
	}
	return nil
}

func (s *Service) loadFileUndo(ctx context.Context, recovery tool.EffectReconciliationContext) (rootRecord, undoRecord, error) {
	run, found, err := s.store.GetRun(recovery.RunID)
	if err != nil || !found {
		return rootRecord{}, undoRecord{}, failure(tool.ErrorSecurityScopeInvalid, "Undo Run is unavailable")
	}
	if err = s.store.CheckWorkspaceExecution(run.WorkspaceID); err != nil {
		return rootRecord{}, undoRecord{}, err
	}
	switch run.Status {
	case domain.RunCompleted, domain.RunFailed, domain.RunCanceled:
	default:
		return rootRecord{}, undoRecord{}, failure("filesystem_undo_unavailable", "Finish or cancel the Run before undoing its file changes")
	}
	storage, ok := s.store.(writeStore)
	if !ok {
		return rootRecord{}, undoRecord{}, errors.New("Undo journal unavailable")
	}
	records, err := storage.ListToolEffects(run.ID)
	if err != nil {
		return rootRecord{}, undoRecord{}, err
	}
	for _, effect := range records {
		if effect.IdempotencyKey != recovery.IdempotencyKey {
			continue
		}
		if effect.RequestHash != recovery.RequestHash || effect.ToolName != recovery.ToolName || effect.ToolCallID != recovery.ToolCallID || effect.StageID != recovery.StageID || effect.TurnID != recovery.TurnID {
			break
		}
		var receipt WriteResult
		var envelope struct {
			Result json.RawMessage `json:"result"`
		}
		data := effect.Result
		if json.Unmarshal(data, &envelope) == nil && len(envelope.Result) > 0 {
			data = envelope.Result
		}
		if json.Unmarshal(data, &receipt) != nil || receipt.UndoExpiresAt == nil || receipt.Operation == "create" || receipt.RootRevision != s.revision {
			break
		}
		if err := validateWritePath(receipt.Path); err != nil {
			return rootRecord{}, undoRecord{}, err
		}
		root, err := s.authorizedRootForWorkspace(run.WorkspaceID, receipt.Root)
		if err != nil {
			return rootRecord{}, undoRecord{}, err
		}
		if root.WriteAccess != "managed_output" || root.UndoRetentionSeconds == 0 {
			break
		}
		return root, undoRecord{Receipt: receipt, WorkspaceID: run.WorkspaceID, Identity: tool.EffectIdentity{Key: effect.IdempotencyKey, RunID: effect.RunID, StageID: effect.StageID, TurnID: effect.TurnID, CallID: effect.ToolCallID, Tool: effect.ToolName, RequestHash: effect.RequestHash, DefinitionRevision: effect.DefinitionRevision}}, nil
	}
	return rootRecord{}, undoRecord{}, failure("filesystem_undo_unavailable", "This change has no retained before-image")
}
