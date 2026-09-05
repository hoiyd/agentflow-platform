package store

import (
	"errors"
	"fmt"
	"strings"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/failure"
)

type ToolEffectVersionConflict struct {
	Expected int64
	Actual   int64
}

func (e *ToolEffectVersionConflict) Error() string {
	return fmt.Sprintf("tool effect version conflict: expected=%d actual=%d", e.Expected, e.Actual)
}

func (e *ToolEffectVersionConflict) FailureInfo() failure.Info {
	return failure.Info{
		Code: "tool_effect_version_conflict", Source: "tool_reconciliation", Category: failure.CategoryValidation,
		Details: map[string]any{"expected_version": e.Expected, "actual_version": e.Actual},
	}
}

type ToolEffectStateConflict struct{ Status string }

func (e *ToolEffectStateConflict) Error() string {
	return "tool effect is not awaiting reconciliation: status=" + e.Status
}

func (e *ToolEffectStateConflict) FailureInfo() failure.Info {
	return failure.Info{Code: "tool_effect_state_conflict", Source: "tool_reconciliation", Category: failure.CategoryValidation}
}

func IsToolEffectConflict(err error) bool {
	var version *ToolEffectVersionConflict
	var state *ToolEffectStateConflict
	return errors.As(err, &version) || errors.As(err, &state)
}

func prepareToolEffectReconciliation(existing domain.ToolEffectRecord, mutation domain.ToolEffectReconciliation) (domain.ToolEffectReconciliation, error) {
	existing.Version = max(existing.Version, 1)
	if strings.TrimSpace(mutation.CommandID) == "" || strings.TrimSpace(mutation.IdempotencyKey) == "" || mutation.ExpectedVersion <= 0 {
		return mutation, errors.New("tool effect reconciliation requires command, effect, and expected version")
	}
	if mutation.IdempotencyKey != existing.IdempotencyKey || mutation.Event.RunID != existing.RunID || mutation.Event.StageID != existing.StageID || strings.TrimSpace(mutation.Event.ID) == "" {
		return mutation, errors.New("tool effect reconciliation event does not match effect")
	}
	payloadVersion, versionOK := payloadInt64(mutation.Event.Payload["expected_version"])
	if mutation.Event.Payload["command_id"] != mutation.CommandID || mutation.Event.Payload["idempotency_key"] != mutation.IdempotencyKey || mutation.Event.Payload["action"] != string(mutation.Action) || !versionOK || payloadVersion != mutation.ExpectedVersion {
		return mutation, errors.New("tool effect reconciliation audit payload does not match command")
	}
	if mutation.ExpectedVersion != existing.Version {
		return mutation, &ToolEffectVersionConflict{Expected: mutation.ExpectedVersion, Actual: existing.Version}
	}
	if existing.Status != domain.ToolEffectNeedsReconciliation {
		return mutation, &ToolEffectStateConflict{Status: string(existing.Status)}
	}
	if !validToolEffectSettlement(mutation) {
		return mutation, errors.New("invalid tool effect reconciliation settlement")
	}
	mutation.Event.Payload["result_version"] = existing.Version + 1
	return mutation, nil
}

func payloadInt64(value any) (int64, bool) {
	switch value := value.(type) {
	case int:
		return int64(value), true
	case int64:
		return value, true
	case float64:
		converted := int64(value)
		return converted, value == float64(converted)
	default:
		return 0, false
	}
}

func validToolEffectSettlement(mutation domain.ToolEffectReconciliation) bool {
	if mutation.Event.Type == domain.EventToolEffectReconciliationFailed {
		return mutation.NextStatus == domain.ToolEffectNeedsReconciliation
	}
	if mutation.Event.Type != domain.EventToolEffectReconciled {
		return false
	}
	switch mutation.Action {
	case domain.ToolEffectConfirmCommitted, domain.ToolEffectRetrySameKey:
		return mutation.NextStatus == domain.ToolEffectCommitted && len(mutation.Result) > 0
	case domain.ToolEffectConfirmFailed:
		return mutation.NextStatus == domain.ToolEffectFailed && len(mutation.Result) == 0
	case domain.ToolEffectCompensate:
		return mutation.NextStatus == domain.ToolEffectCompensated && len(mutation.Result) == 0
	default:
		return false
	}
}
