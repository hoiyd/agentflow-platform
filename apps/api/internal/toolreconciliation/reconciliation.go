package toolreconciliation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/failure"
	"agentflow-platform/apps/api/internal/toolpolicy"
	"agentflow-platform/apps/api/internal/tools"
)

const (
	ReconciliationInvalid     = "invalid_command"
	ReconciliationNotFound    = "effect_not_found"
	ReconciliationConflict    = "state_conflict"
	ReconciliationUnavailable = "capability_unavailable"
	ReconciliationMismatch    = "definition_mismatch"
)

type ReconciliationError struct {
	Code    string
	Message string
}

func (e *ReconciliationError) Error() string { return e.Message }

func (e *ReconciliationError) FailureInfo() failure.Info {
	category := failure.CategoryValidation
	if e.Code == ReconciliationNotFound {
		category = failure.CategoryNotFound
	}
	return failure.Info{Code: "tool_reconciliation_" + e.Code, Source: "tool_reconciliation", Category: category}
}

type ToolEffectReconciliationCommand struct {
	CommandID       string                                `json:"command_id"`
	Action          domain.ToolEffectReconciliationAction `json:"action"`
	ExpectedVersion int64                                 `json:"expected_version"`
	Actor           string                                `json:"actor"`
	Reason          string                                `json:"reason"`
	Result          json.RawMessage                       `json:"result,omitempty"`
}

type ToolEffectView struct {
	domain.ToolEffectSummary
	AvailableActions []domain.ToolEffectReconciliationAction `json:"available_actions"`
}

type ToolEffectReconciliationOutcome struct {
	CommandID string         `json:"command_id"`
	Outcome   string         `json:"outcome"`
	Applied   bool           `json:"applied"`
	Effect    ToolEffectView `json:"effect"`
}

type effectReconciliationStore interface {
	ListToolEffects(runID string) ([]domain.ToolEffectRecord, error)
	ListRunEvents(runID string) ([]domain.RunEvent, error)
	CommitToolEffectReconciliation(domain.ToolEffectReconciliation) (domain.ToolEffectRecord, domain.RunEvent, bool, error)
}

func NewToolEffectViews(catalog *tools.Catalog, records []domain.ToolEffectRecord) []ToolEffectView {
	items := make([]ToolEffectView, 0, len(records))
	for _, summary := range domain.SummarizeToolEffects(records) {
		items = append(items, ToolEffectView{ToolEffectSummary: summary, AvailableActions: availableReconciliationActions(catalog, summary)})
	}
	return items
}

func ValidToolEffectStatus(status domain.ToolEffectStatus) bool {
	switch status {
	case domain.ToolEffectPrepared, domain.ToolEffectExecuting, domain.ToolEffectCommitted,
		domain.ToolEffectFailed, domain.ToolEffectCompensated, domain.ToolEffectNeedsReconciliation:
		return true
	default:
		return false
	}
}

func ReconcileToolEffect(ctx context.Context, catalog *tools.Catalog, target effectReconciliationStore, run domain.Run, idempotencyKey string, command ToolEffectReconciliationCommand) (ToolEffectReconciliationOutcome, error) {
	command = normalizeReconciliationCommand(command)
	if err := validateReconciliationCommand(idempotencyKey, command); err != nil {
		return ToolEffectReconciliationOutcome{}, err
	}
	records, err := target.ListToolEffects(run.ID)
	if err != nil {
		return ToolEffectReconciliationOutcome{}, err
	}
	record, found := findToolEffect(records, idempotencyKey)
	if !found {
		return ToolEffectReconciliationOutcome{}, &ReconciliationError{Code: ReconciliationNotFound, Message: "tool effect not found"}
	}
	if duplicate, err := reconciliationCommandOutcome(target, catalog, run.ID, record, command.CommandID); err != nil || duplicate != nil {
		if duplicate != nil {
			return *duplicate, nil
		}
		return ToolEffectReconciliationOutcome{}, err
	}
	record.Version = max(record.Version, 1)
	if record.Status != domain.ToolEffectNeedsReconciliation {
		return ToolEffectReconciliationOutcome{}, &ReconciliationError{Code: ReconciliationConflict, Message: "tool effect is not awaiting reconciliation"}
	}
	if command.ExpectedVersion != record.Version {
		return ToolEffectReconciliationOutcome{}, &ReconciliationError{Code: ReconciliationConflict, Message: fmt.Sprintf("tool effect version conflict: expected=%d actual=%d", command.ExpectedVersion, record.Version)}
	}

	nextStatus, result, operationErr := executeReconciliationAction(ctx, catalog, record, command)
	var commandErr *ReconciliationError
	if errors.As(operationErr, &commandErr) {
		return ToolEffectReconciliationOutcome{}, commandErr
	}
	eventType, outcome, errorMessage := domain.EventToolEffectReconciled, "completed", ""
	if operationErr != nil {
		eventType, outcome = domain.EventToolEffectReconciliationFailed, "failed"
		nextStatus, result, errorMessage = domain.ToolEffectNeedsReconciliation, nil, boundedReconciliationText(operationErr.Error(), 512)
	} else if command.Action == domain.ToolEffectConfirmFailed {
		errorMessage = command.Reason
	}
	auditEvent, err := event.NewRunEvent(eventType, event.EventMetadata{
		ConversationID: run.ConversationID, RunID: run.ID, StageID: record.StageID, TurnID: record.TurnID,
	}, event.ToolEffectReconciliationPayload{
		CommandID: command.CommandID, IdempotencyKey: record.IdempotencyKey,
		ToolCallID: record.ToolCallID, ToolName: record.ToolName, Action: string(command.Action),
		Actor: command.Actor, Reason: command.Reason, ExpectedVersion: command.ExpectedVersion,
		Outcome: outcome, Status: string(nextStatus), Error: errorMessage,
	})
	if err != nil {
		return ToolEffectReconciliationOutcome{}, err
	}
	auditEvent.ID = "event_tool_effect_reconciliation_" + hashExecutionIdentity(run.ID, record.IdempotencyKey, command.CommandID)
	updated, _, applied, err := target.CommitToolEffectReconciliation(domain.ToolEffectReconciliation{
		CommandID: command.CommandID, IdempotencyKey: record.IdempotencyKey,
		ExpectedVersion: command.ExpectedVersion, Action: command.Action,
		NextStatus: nextStatus, Result: result, Error: errorMessage, Event: auditEvent,
	})
	if err != nil {
		return ToolEffectReconciliationOutcome{}, err
	}
	return ToolEffectReconciliationOutcome{
		CommandID: command.CommandID, Outcome: outcome, Applied: applied,
		Effect: NewToolEffectViews(catalog, []domain.ToolEffectRecord{updated})[0],
	}, nil
}

func executeReconciliationAction(ctx context.Context, catalog *tools.Catalog, record domain.ToolEffectRecord, command ToolEffectReconciliationCommand) (domain.ToolEffectStatus, []byte, error) {
	switch command.Action {
	case domain.ToolEffectConfirmCommitted:
		var result any
		if err := json.Unmarshal(command.Result, &result); err != nil {
			return "", nil, &ReconciliationError{Code: ReconciliationInvalid, Message: "confirm_committed requires a valid JSON result"}
		}
		encoded, err := encodeReconciledResult(record, result)
		return domain.ToolEffectCommitted, encoded, err
	case domain.ToolEffectConfirmFailed:
		return domain.ToolEffectFailed, nil, nil
	case domain.ToolEffectRetrySameKey, domain.ToolEffectCompensate:
		binding, err := reconciliationBinding(catalog, record)
		if err != nil {
			return "", nil, err
		}
		recovery := tools.EffectReconciliationContext{
			CommandID: command.CommandID, IdempotencyKey: record.IdempotencyKey,
			CompensationKey: "tool_compensation_" + hashExecutionIdentity(record.IdempotencyKey, command.CommandID),
			RunID:           record.RunID, StageID: record.StageID, TurnID: record.TurnID,
			ToolCallID: record.ToolCallID, ToolName: record.ToolName, RequestHash: record.RequestHash,
		}
		if command.Action == domain.ToolEffectRetrySameKey {
			if !binding.Descriptor.SideEffect.RetryWithSameKey || binding.Reconciliation.RetryWithSameKey == nil {
				return "", nil, &ReconciliationError{Code: ReconciliationUnavailable, Message: "tool does not support retry_with_same_key"}
			}
			value, err := callEffectRetry(ctx, binding.Reconciliation.RetryWithSameKey, recovery)
			if err != nil {
				return "", nil, err
			}
			encoded, err := encodeReconciledResult(record, value)
			return domain.ToolEffectCommitted, encoded, err
		}
		if !binding.Descriptor.SideEffect.Compensate || binding.Reconciliation.Compensate == nil || binding.Descriptor.Security.Reversibility == toolpolicy.Irreversible {
			return "", nil, &ReconciliationError{Code: ReconciliationUnavailable, Message: "tool does not support compensation"}
		}
		return domain.ToolEffectCompensated, nil, callEffectCompensation(ctx, binding.Reconciliation.Compensate, recovery)
	default:
		return "", nil, &ReconciliationError{Code: ReconciliationInvalid, Message: "unsupported reconciliation action"}
	}
}

func reconciliationBinding(catalog *tools.Catalog, record domain.ToolEffectRecord) (tools.Binding, error) {
	if catalog == nil {
		return tools.Binding{}, &ReconciliationError{Code: ReconciliationUnavailable, Message: "tool catalog is unavailable"}
	}
	binding, ok := catalog.Installed(record.ToolName)
	if !ok || binding.Descriptor.SideEffect.Mode != tools.SideEffectExternal {
		return tools.Binding{}, &ReconciliationError{Code: ReconciliationUnavailable, Message: "tool reconciliation binding is unavailable"}
	}
	if record.DefinitionRevision == "" || binding.Descriptor.DefinitionRevision != record.DefinitionRevision {
		return tools.Binding{}, &ReconciliationError{Code: ReconciliationMismatch, Message: "tool definition revision does not match the uncertain effect"}
	}
	return binding, nil
}

func availableReconciliationActions(catalog *tools.Catalog, effect domain.ToolEffectSummary) []domain.ToolEffectReconciliationAction {
	if effect.Status != domain.ToolEffectNeedsReconciliation {
		return []domain.ToolEffectReconciliationAction{}
	}
	actions := []domain.ToolEffectReconciliationAction{domain.ToolEffectConfirmCommitted, domain.ToolEffectConfirmFailed}
	binding, err := reconciliationBinding(catalog, domain.ToolEffectRecord{
		ToolName: effect.ToolName, DefinitionRevision: effect.DefinitionRevision,
	})
	if err != nil {
		return actions
	}
	if binding.Descriptor.SideEffect.RetryWithSameKey && binding.Reconciliation.RetryWithSameKey != nil {
		actions = append(actions, domain.ToolEffectRetrySameKey)
	}
	if binding.Descriptor.SideEffect.Compensate && binding.Reconciliation.Compensate != nil && binding.Descriptor.Security.Reversibility != toolpolicy.Irreversible {
		actions = append(actions, domain.ToolEffectCompensate)
	}
	return actions
}

func reconciliationCommandOutcome(target effectReconciliationStore, catalog *tools.Catalog, runID string, record domain.ToolEffectRecord, commandID string) (*ToolEffectReconciliationOutcome, error) {
	events, err := target.ListRunEvents(runID)
	if err != nil {
		return nil, err
	}
	for _, item := range events {
		if item.Type != domain.EventToolEffectReconciled && item.Type != domain.EventToolEffectReconciliationFailed {
			continue
		}
		if item.Payload["command_id"] == commandID && item.Payload["idempotency_key"] == record.IdempotencyKey {
			outcome, _ := item.Payload["outcome"].(string)
			view := NewToolEffectViews(catalog, []domain.ToolEffectRecord{record})[0]
			return &ToolEffectReconciliationOutcome{CommandID: commandID, Outcome: outcome, Applied: false, Effect: view}, nil
		}
	}
	return nil, nil
}

func findToolEffect(records []domain.ToolEffectRecord, idempotencyKey string) (domain.ToolEffectRecord, bool) {
	for _, item := range records {
		if item.IdempotencyKey == idempotencyKey {
			return item, true
		}
	}
	return domain.ToolEffectRecord{}, false
}

func normalizeReconciliationCommand(command ToolEffectReconciliationCommand) ToolEffectReconciliationCommand {
	command.CommandID = strings.TrimSpace(command.CommandID)
	command.Actor = strings.TrimSpace(command.Actor)
	command.Reason = strings.TrimSpace(command.Reason)
	command.Result = bytes.TrimSpace(command.Result)
	return command
}

func validateReconciliationCommand(idempotencyKey string, command ToolEffectReconciliationCommand) error {
	if strings.TrimSpace(idempotencyKey) == "" || command.CommandID == "" || command.Actor == "" || command.Reason == "" || command.ExpectedVersion <= 0 {
		return &ReconciliationError{Code: ReconciliationInvalid, Message: "reconciliation requires effect, command_id, expected_version, actor, and reason"}
	}
	if len(command.CommandID) > 128 || len(command.Actor) > 128 || len(command.Reason) > 512 {
		return &ReconciliationError{Code: ReconciliationInvalid, Message: "reconciliation command metadata is too large"}
	}
	if command.Action == domain.ToolEffectConfirmCommitted {
		if len(command.Result) == 0 || len(command.Result) > tools.DefaultMaxResultBytes || !json.Valid(command.Result) {
			return &ReconciliationError{Code: ReconciliationInvalid, Message: "confirm_committed requires a bounded JSON result"}
		}
	} else if len(command.Result) != 0 {
		return &ReconciliationError{Code: ReconciliationInvalid, Message: "result is only valid for confirm_committed"}
	}
	switch command.Action {
	case domain.ToolEffectConfirmCommitted, domain.ToolEffectConfirmFailed, domain.ToolEffectRetrySameKey, domain.ToolEffectCompensate:
	default:
		return &ReconciliationError{Code: ReconciliationInvalid, Message: "unsupported reconciliation action"}
	}
	return nil
}

func encodeReconciledResult(record domain.ToolEffectRecord, value any) ([]byte, error) {
	encoded, err := json.Marshal(tools.ExecutionResult{
		CallID: record.ToolCallID, Tool: record.ToolName, Result: value,
		DefinitionRevision: record.DefinitionRevision,
	})
	if err != nil || len(encoded) > tools.DefaultMaxResultBytes {
		return nil, fmt.Errorf("reconciled Tool result is invalid or exceeds %d bytes", tools.DefaultMaxResultBytes)
	}
	return encoded, nil
}

func callEffectRetry(ctx context.Context, callback func(context.Context, tools.EffectReconciliationContext) (any, error), recovery tools.EffectReconciliationContext) (result any, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("tool retry panicked: %v", recovered)
		}
	}()
	return callback(ctx, recovery)
}

func callEffectCompensation(ctx context.Context, callback func(context.Context, tools.EffectReconciliationContext) error, recovery tools.EffectReconciliationContext) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("tool compensation panicked: %v", recovered)
		}
	}()
	return callback(ctx, recovery)
}

func boundedReconciliationText(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len(value) <= limit {
		return value
	}
	value = value[:limit]
	for len(value) > 0 && !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}

func hashExecutionIdentity(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return fmt.Sprintf("%x", sum[:])
}
