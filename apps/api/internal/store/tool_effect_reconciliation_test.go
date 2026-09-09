package store

import (
	"errors"

	"testing"

	"agentflow-platform/apps/api/internal/domain"
)

func TestPrepareToolEffectReconciliationValidatesCASAndSettlement(t *testing.T) {
	effect := domain.ToolEffectRecord{
		IdempotencyKey: "effect-1", Version: 2, RunID: "run-1", StageID: "stage-1",
		Status: domain.ToolEffectNeedsReconciliation,
	}
	valid := reconciliationMutation(effect, domain.ToolEffectConfirmFailed, domain.ToolEffectFailed, nil)
	prepared, err := PrepareToolEffectReconciliation(effect, valid)
	if err != nil || prepared.Event.Payload["result_version"] != int64(3) {
		t.Fatalf("prepare valid mutation: %#v err=%v", prepared, err)
	}

	tests := []struct {
		name   string
		mutate func(*domain.ToolEffectRecord, *domain.ToolEffectReconciliation)
		match  func(error) bool
	}{
		{name: "missing command", mutate: func(_ *domain.ToolEffectRecord, item *domain.ToolEffectReconciliation) { item.CommandID = "" }},
		{name: "event identity", mutate: func(_ *domain.ToolEffectRecord, item *domain.ToolEffectReconciliation) { item.Event.RunID = "other" }},
		{name: "audit payload", mutate: func(_ *domain.ToolEffectRecord, item *domain.ToolEffectReconciliation) {
			item.Event.Payload["action"] = "other"
		}},
		{name: "version", mutate: func(_ *domain.ToolEffectRecord, item *domain.ToolEffectReconciliation) {
			item.ExpectedVersion++
			item.Event.Payload["expected_version"] = item.ExpectedVersion
		}, match: IsToolEffectConflict},
		{name: "state", mutate: func(effect *domain.ToolEffectRecord, _ *domain.ToolEffectReconciliation) {
			effect.Status = domain.ToolEffectCommitted
		}, match: IsToolEffectConflict},
		{name: "settlement", mutate: func(_ *domain.ToolEffectRecord, item *domain.ToolEffectReconciliation) {
			item.NextStatus = domain.ToolEffectCommitted
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			current, mutation := effect, reconciliationMutation(effect, domain.ToolEffectConfirmFailed, domain.ToolEffectFailed, nil)
			test.mutate(&current, &mutation)
			_, err := PrepareToolEffectReconciliation(current, mutation)
			if err == nil || test.match != nil && !test.match(err) {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestToolEffectReconciliationSettlementVariantsAndErrors(t *testing.T) {
	effect := domain.ToolEffectRecord{IdempotencyKey: "effect-1", Version: 2, RunID: "run-1", StageID: "stage-1", Status: domain.ToolEffectNeedsReconciliation}
	for _, test := range []struct {
		action domain.ToolEffectReconciliationAction
		status domain.ToolEffectStatus
		result []byte
		typeID domain.RunEventType
	}{
		{domain.ToolEffectConfirmCommitted, domain.ToolEffectCommitted, []byte(`{"ok":true}`), domain.EventToolEffectReconciled},
		{domain.ToolEffectRetrySameKey, domain.ToolEffectCommitted, []byte(`{"ok":true}`), domain.EventToolEffectReconciled},
		{domain.ToolEffectCompensate, domain.ToolEffectCompensated, nil, domain.EventToolEffectReconciled},
		{domain.ToolEffectRetrySameKey, domain.ToolEffectNeedsReconciliation, nil, domain.EventToolEffectReconciliationFailed},
	} {
		effect.Status = domain.ToolEffectReconciling
		mutation := reconciliationMutation(effect, test.action, test.status, test.result)
		mutation.Event.Type = test.typeID
		if _, err := PrepareToolEffectReconciliation(effect, mutation); err != nil {
			t.Fatalf("action %s: %v", test.action, err)
		}
	}

	version := &ToolEffectVersionConflict{Expected: 1, Actual: 2}
	if version.Error() == "" || version.FailureInfo().Details["actual_version"] != int64(2) || !IsToolEffectConflict(version) {
		t.Fatalf("invalid version conflict: %#v", version)
	}
	state := &ToolEffectStateConflict{Status: "committed"}
	if state.Error() == "" || state.FailureInfo().Code == "" || !IsToolEffectConflict(state) || IsToolEffectConflict(errors.New("other")) {
		t.Fatalf("invalid state conflict: %#v", state)
	}
}

func TestPayloadInt64AcceptsJSONAndNativeIntegers(t *testing.T) {
	for _, value := range []any{2, int64(2), float64(2)} {
		if result, ok := payloadInt64(value); !ok || result != 2 {
			t.Fatalf("payloadInt64(%T)=%d,%t", value, result, ok)
		}
	}
	for _, value := range []any{2.5, "2"} {
		if _, ok := payloadInt64(value); ok {
			t.Fatalf("payloadInt64 accepted %v", value)
		}
	}
}

func TestReconciliationRequiresAUniqueClaimBeforeCallbackSettlement(t *testing.T) {
	effect := domain.ToolEffectRecord{IdempotencyKey: "effect", RunID: "run", StageID: "stage", Version: 2, Status: domain.ToolEffectNeedsReconciliation}
	mutation := reconciliationMutation(effect, domain.ToolEffectRetrySameKey, domain.ToolEffectCommitted, []byte(`{}`))
	if _, err := PrepareToolEffectReconciliation(effect, mutation); !IsToolEffectConflict(err) {
		t.Fatalf("callback settled without a claim: %v", err)
	}
	mutation.Event.Type = domain.EventToolEffectReconciliationStarted
	mutation.Event.Payload["command_hash"] = "hash"
	mutation.Event.Payload["outcome"] = "pending"
	mutation.NextStatus, mutation.Result = domain.ToolEffectReconciling, nil
	if _, err := PrepareToolEffectReconciliation(effect, mutation); err != nil {
		t.Fatal(err)
	}
	effect.Status = domain.ToolEffectReconciling
	if _, err := PrepareToolEffectReconciliation(effect, mutation); !IsToolEffectConflict(err) {
		t.Fatalf("outstanding claim reclaimed: %v", err)
	}
	mutation.Event.Payload["command_hash"] = ""
	if validToolEffectSettlement(mutation) {
		t.Fatal("claim without command identity accepted")
	}
}

func reconciliationMutation(effect domain.ToolEffectRecord, action domain.ToolEffectReconciliationAction, status domain.ToolEffectStatus, result []byte) domain.ToolEffectReconciliation {
	return domain.ToolEffectReconciliation{
		CommandID: "command-1", IdempotencyKey: effect.IdempotencyKey, ExpectedVersion: effect.Version,
		Action: action, NextStatus: status, Result: result,
		Event: domain.RunEvent{
			ID: "event-1", RunID: effect.RunID, StageID: effect.StageID, Type: domain.EventToolEffectReconciled,
			Payload: map[string]any{
				"command_id": "command-1", "idempotency_key": effect.IdempotencyKey,
				"action": string(action), "expected_version": effect.Version,
			},
		},
	}
}
