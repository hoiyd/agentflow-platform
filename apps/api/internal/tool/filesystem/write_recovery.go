package filesystem

import (
	"context"
	"encoding/json"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/tool"
)

// The retry hook only verifies already-published evidence. It never repeats a
// filesystem write; absent/changed evidence requires operator disposition.
func (s *Service) verifyPublished(ctx context.Context, recovery tool.EffectReconciliationContext) (any, error) {
	storage, ok := s.store.(writeStore)
	if !ok {
		return nil, failure(tool.ErrorEffectReconciliation, "Filesystem recovery storage is unavailable")
	}
	run, found, err := s.store.GetRun(recovery.RunID)
	if err != nil || !found {
		return nil, failure(tool.ErrorSecurityScopeInvalid, "Filesystem recovery Run is unavailable")
	}
	ctx = event.WithScope(ctx, event.Scope{RunID: run.ID, ConversationID: run.ConversationID, StageID: recovery.StageID, TurnID: recovery.TurnID})
	records, err := storage.ListToolEffects(run.ID)
	if err != nil {
		return nil, err
	}
	var record domain.ToolEffectRecord
	for _, candidate := range records {
		if candidate.IdempotencyKey == recovery.IdempotencyKey {
			record = candidate
			break
		}
	}
	if record.IdempotencyKey == "" || record.Status != domain.ToolEffectReconciling || record.RequestHash != recovery.RequestHash || record.ToolCallID != recovery.ToolCallID || record.ToolName != recovery.ToolName || record.StageID != recovery.StageID || record.TurnID != recovery.TurnID {
		return nil, failure(tool.ErrorEffectReconciliation, "Filesystem recovery does not match the prepared effect")
	}
	var receipt WriteResult
	if json.Unmarshal(record.Result, &receipt) != nil || receipt.NewHash == "" || receipt.FileID == "" || receipt.RootRevision != s.revision {
		return nil, failure(tool.ErrorEffectReconciliation, "Filesystem publication evidence is unavailable or changed")
	}
	if err = validateWritePath(receipt.Path); err != nil {
		return nil, err
	}
	root, err := s.authorizedRoot(ctx, receipt.Root)
	if err != nil {
		return nil, err
	}
	if root.WriteAccess != "managed_output" {
		return nil, failure(tool.ErrorSecurityPolicyDenied, "Output root is no longer writable")
	}
	dir, err := lockOutput(ctx, root)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	current, err := inspectOutput(dir, receipt.Path)
	if err != nil {
		return nil, err
	}
	if !current.exists || current.hash != receipt.NewHash || current.identity != receipt.FileID {
		return nil, failure(tool.ErrorEffectReconciliation, "Published report identity/hash cannot be confirmed; manual reconciliation is required")
	}
	if err = dir.Sync(); err != nil {
		return nil, failure(tool.ErrorEffectReconciliation, "Published report directory durability remains uncertain")
	}
	return receipt, nil
}
