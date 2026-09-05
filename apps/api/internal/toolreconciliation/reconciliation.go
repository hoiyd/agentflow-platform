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
	"agentflow-platform/apps/api/internal/redaction"
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
		domain.ToolEffectFailed, domain.ToolEffectCompensated, domain.ToolEffectNeedsReconciliation, domain.ToolEffectReconciling:
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
	if err := ctx.Err(); err != nil {
		return ToolEffectReconciliationOutcome{}, err
	}
	encodedCommand, _ := json.Marshal(command)
	commandHash := hashExecutionIdentity(string(encodedCommand))
	records, err := target.ListToolEffects(run.ID)
	if err != nil {
		return ToolEffectReconciliationOutcome{}, err
	}
	record, found := findToolEffect(records, idempotencyKey)
	if !found {
		return ToolEffectReconciliationOutcome{}, &ReconciliationError{Code: ReconciliationNotFound, Message: "tool effect not found"}
	}
	if duplicate, err := reconciliationCommandOutcome(target, catalog, run.ID, record, command.CommandID, commandHash); err != nil || duplicate != nil {
		if duplicate != nil {
			return *duplicate, nil
		}
		return ToolEffectReconciliationOutcome{}, err
	}
	record.Version = max(record.Version, 1)
	external := command.Action == domain.ToolEffectRetrySameKey || command.Action == domain.ToolEffectCompensate
	if record.Status != domain.ToolEffectNeedsReconciliation && !(record.Status == domain.ToolEffectReconciling && !external) {
		return ToolEffectReconciliationOutcome{}, &ReconciliationError{Code: ReconciliationConflict, Message: "tool effect is not awaiting reconciliation"}
	}
	if command.ExpectedVersion != record.Version {
		return ToolEffectReconciliationOutcome{}, &ReconciliationError{Code: ReconciliationConflict, Message: fmt.Sprintf("tool effect version conflict: expected=%d actual=%d", command.ExpectedVersion, record.Version)}
	}

	if external {
		binding, err := authorizedReconciliationBinding(catalog, record, command.Action)
		if err != nil {
			return ToolEffectReconciliationOutcome{}, err
		}
		claimed, err := commitReconciliation(target, run, record, command, commandHash,
			reconciliationSettlement{eventType: domain.EventToolEffectReconciliationStarted, outcome: "pending", status: domain.ToolEffectReconciling})
		if err != nil {
			return ToolEffectReconciliationOutcome{}, err
		}
		if !claimed.Applied {
			duplicate, err := reconciliationCommandOutcome(target, catalog, run.ID, record, command.CommandID, commandHash)
			if err != nil || duplicate != nil {
				if duplicate != nil {
					return *duplicate, nil
				}
				return ToolEffectReconciliationOutcome{}, err
			}
			return claimed, nil
		}
		record.Version = claimed.Effect.Version
		record.Status = domain.ToolEffectReconciling
		// The claim survives process death. A deadline never releases it for a
		// second callback: Go cannot stop a Binding that ignores cancellation.
		timeout := binding.Policy.Timeout
		if timeout <= 0 || timeout > tools.DefaultExecutionTimeout {
			timeout = tools.DefaultExecutionTimeout
		}
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	nextStatus, result, operationErr := boundedReconciliationAction(ctx, catalog, record, command)
	eventType, outcome, errorMessage := domain.EventToolEffectReconciled, "completed", ""
	if operationErr != nil {
		eventType, outcome = domain.EventToolEffectReconciliationFailed, "failed"
		nextStatus, result, errorMessage = domain.ToolEffectNeedsReconciliation, nil, boundedReconciliationText(operationErr.Error(), 512)
		if !external {
			// A failed manual confirmation must not release an outstanding callback.
			nextStatus = record.Status
		}
		if external && (errors.Is(operationErr, context.DeadlineExceeded) || errors.Is(operationErr, context.Canceled)) {
			nextStatus = domain.ToolEffectReconciling
		}
	} else if command.Action == domain.ToolEffectConfirmFailed {
		errorMessage = command.Reason
	}
	settled, err := commitReconciliation(target, run, record, command, commandHash,
		reconciliationSettlement{eventType, outcome, nextStatus, result, errorMessage})
	if err == nil {
		settled.Effect.AvailableActions = availableReconciliationActions(catalog, settled.Effect.ToolEffectSummary)
	}
	return settled, err
}

type reconciliationSettlement struct {
	eventType domain.RunEventType
	outcome   string
	status    domain.ToolEffectStatus
	result    []byte
	message   string
}

func commitReconciliation(target effectReconciliationStore, run domain.Run, record domain.ToolEffectRecord, command ToolEffectReconciliationCommand, commandHash string, settlement reconciliationSettlement) (ToolEffectReconciliationOutcome, error) {
	auditEvent, err := event.NewRunEvent(settlement.eventType, event.EventMetadata{
		ConversationID: run.ConversationID, RunID: run.ID, StageID: record.StageID, TurnID: record.TurnID,
	}, event.ToolEffectReconciliationPayload{
		CommandID: command.CommandID, CommandHash: commandHash, IdempotencyKey: record.IdempotencyKey,
		ToolCallID: record.ToolCallID, ToolName: record.ToolName, Action: string(command.Action),
		Actor: boundedReconciliationText(command.Actor, 128), Reason: boundedReconciliationText(command.Reason, 512), ExpectedVersion: record.Version,
		Outcome: settlement.outcome, Status: string(settlement.status), Error: boundedReconciliationText(settlement.message, 512),
	})
	if err != nil {
		return ToolEffectReconciliationOutcome{}, err
	}
	auditEvent.ID = "event_tool_effect_reconciliation_" + hashExecutionIdentity(run.ID, record.IdempotencyKey, command.CommandID)
	if settlement.eventType == domain.EventToolEffectReconciliationStarted {
		auditEvent.ID += "_claim"
	}
	updated, _, applied, err := target.CommitToolEffectReconciliation(domain.ToolEffectReconciliation{
		CommandID: command.CommandID, IdempotencyKey: record.IdempotencyKey,
		ExpectedVersion: record.Version, Action: command.Action,
		NextStatus: settlement.status, Result: settlement.result, Error: boundedReconciliationText(settlement.message, 512), Event: auditEvent,
	})
	if err != nil {
		return ToolEffectReconciliationOutcome{}, err
	}
	return ToolEffectReconciliationOutcome{
		CommandID: command.CommandID, Outcome: settlement.outcome, Applied: applied,
		Effect: NewToolEffectViews(nil, []domain.ToolEffectRecord{updated})[0],
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
		binding, err := authorizedReconciliationBinding(catalog, record, command.Action)
		if err != nil {
			return "", nil, err
		}
		recovery := tools.EffectReconciliationContext{
			CommandID: command.CommandID, IdempotencyKey: record.IdempotencyKey,
			CompensationKey: "tool_compensation_" + hashExecutionIdentity(record.IdempotencyKey),
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
			if err == nil && binding.Policy.MaxResultBytes > 0 && len(encoded) > binding.Policy.MaxResultBytes {
				return "", nil, errors.New("reconciled Tool result exceeds Binding result limit")
			}
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
	binding, ok := catalog.Resolve(record.ToolName)
	if !ok || binding.Descriptor.SideEffect.Mode != tools.SideEffectExternal {
		return tools.Binding{}, &ReconciliationError{Code: ReconciliationUnavailable, Message: "tool reconciliation binding is unavailable"}
	}
	if record.DefinitionRevision == "" || binding.Descriptor.DefinitionRevision != record.DefinitionRevision {
		return tools.Binding{}, &ReconciliationError{Code: ReconciliationMismatch, Message: "tool definition revision does not match the uncertain effect"}
	}
	return binding, nil
}

func availableReconciliationActions(catalog *tools.Catalog, effect domain.ToolEffectSummary) []domain.ToolEffectReconciliationAction {
	if effect.Status != domain.ToolEffectNeedsReconciliation && effect.Status != domain.ToolEffectReconciling {
		return []domain.ToolEffectReconciliationAction{}
	}
	actions := []domain.ToolEffectReconciliationAction{domain.ToolEffectConfirmCommitted, domain.ToolEffectConfirmFailed}
	if effect.Status == domain.ToolEffectReconciling {
		return actions
	}
	for _, action := range []domain.ToolEffectReconciliationAction{domain.ToolEffectRetrySameKey, domain.ToolEffectCompensate} {
		if _, err := authorizedReconciliationBinding(catalog, domain.ToolEffectRecord{
			ToolName: effect.ToolName, DefinitionRevision: effect.DefinitionRevision,
		}, action); err == nil {
			actions = append(actions, action)
		}
	}
	return actions
}

func reconciliationCommandOutcome(target effectReconciliationStore, catalog *tools.Catalog, runID string, record domain.ToolEffectRecord, commandID, commandHash string) (*ToolEffectReconciliationOutcome, error) {
	events, err := target.ListRunEvents(runID)
	if err != nil {
		return nil, err
	}
	for index := len(events) - 1; index >= 0; index-- {
		item := events[index]
		if item.Type != domain.EventToolEffectReconciliationStarted && item.Type != domain.EventToolEffectReconciled && item.Type != domain.EventToolEffectReconciliationFailed {
			continue
		}
		if item.Payload["command_id"] == commandID && item.Payload["idempotency_key"] == record.IdempotencyKey {
			if item.Payload["command_hash"] != commandHash {
				return nil, &ReconciliationError{Code: ReconciliationConflict, Message: "command_id is already bound to a different or unverifiable command"}
			}
			outcome, _ := item.Payload["outcome"].(string)
			// Events may have advanced since the initial read (including a concurrent
			// operator confirmation). Return current effect state, not the stale copy.
			records, err := target.ListToolEffects(runID)
			if err != nil {
				return nil, err
			}
			current, found := findToolEffect(records, record.IdempotencyKey)
			if !found {
				return nil, &ReconciliationError{Code: ReconciliationNotFound, Message: "tool effect not found"}
			}
			record = current
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
	if _, count := redaction.Text(command.CommandID); count > 0 {
		return &ReconciliationError{Code: ReconciliationInvalid, Message: "command_id must be an opaque identifier without credentials"}
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
	encoded, _, err = redaction.JSON(encoded)
	if err != nil || len(encoded) > tools.DefaultMaxResultBytes {
		return nil, errors.New("reconciled Tool result cannot be safely persisted")
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
	value, _ = redaction.Text(value)
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
