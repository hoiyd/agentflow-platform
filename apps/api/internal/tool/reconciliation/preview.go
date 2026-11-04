package reconciliation

import (
	"context"
	"errors"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/tool"
)

// PreviewCompensation checks current grants and delegates only metadata reads to
// the Binding. Execution repeats these checks; a preview is not a write grant.
func PreviewCompensation(ctx context.Context, catalog *tool.Catalog, record domain.ToolEffectRecord) (preview tool.CompensationPreview, err error) {
	binding, err := authorizedReconciliationBinding(catalog, record, domain.ToolEffectCompensate)
	if err != nil {
		return preview, err
	}
	if record.Status != domain.ToolEffectCommitted || binding.Reconciliation.PreviewCompensation == nil {
		return preview, &ReconciliationError{Code: ReconciliationUnavailable, Message: "This effect has no committed-change preview"}
	}
	ctx, cancel := context.WithTimeout(ctx, tool.DefaultExecutionTimeout)
	defer cancel()
	defer func() {
		if recover() != nil {
			err = errors.New("compensation preview panicked")
		}
	}()
	preview, err = binding.Reconciliation.PreviewCompensation(ctx, tool.EffectReconciliationContext{
		IdempotencyKey: record.IdempotencyKey, RunID: record.RunID, StageID: record.StageID, TurnID: record.TurnID,
		ToolCallID: record.ToolCallID, ToolName: record.ToolName, RequestHash: record.RequestHash,
	})
	if err == nil {
		err = ctx.Err()
	}
	return preview, err
}
