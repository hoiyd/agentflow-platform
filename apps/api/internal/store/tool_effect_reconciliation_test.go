package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
)

func TestPrepareToolEffectReconciliationValidatesCASAndSettlement(t *testing.T) {
	effect := domain.ToolEffectRecord{
		IdempotencyKey: "effect-1", Version: 2, RunID: "run-1", StageID: "stage-1",
		Status: domain.ToolEffectNeedsReconciliation,
	}
	valid := reconciliationMutation(effect, domain.ToolEffectConfirmFailed, domain.ToolEffectFailed, nil)
	prepared, err := prepareToolEffectReconciliation(effect, valid)
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
			_, err := prepareToolEffectReconciliation(current, mutation)
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
		mutation := reconciliationMutation(effect, test.action, test.status, test.result)
		mutation.Event.Type = test.typeID
		if _, err := prepareToolEffectReconciliation(effect, mutation); err != nil {
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

func TestFileStoreToolEffectReconciliationIsAtomicAndIdempotent(t *testing.T) {
	fileStore, run := checkpointFileTestRun(t)
	effect := beginUncertainEffect(t, fileStore, run, "effect-1")
	mutation := reconciliationMutation(effect, domain.ToolEffectConfirmFailed, domain.ToolEffectFailed, nil)
	conflict := mutation
	conflict.ExpectedVersion++
	conflict.Event.Payload["expected_version"] = conflict.ExpectedVersion
	if _, _, _, err := fileStore.CommitToolEffectReconciliation(conflict); !IsToolEffectConflict(err) {
		t.Fatalf("expected version conflict, got %v", err)
	}
	mutation.Event.Payload["expected_version"] = mutation.ExpectedVersion
	invalidEvent := mutation
	invalidEvent.Event.SchemaVersion = domain.CurrentRunEventSchemaVersion + 1
	if _, _, _, err := fileStore.CommitToolEffectReconciliation(invalidEvent); err == nil {
		t.Fatal("expected event schema failure")
	}
	settled, _, applied, err := fileStore.CommitToolEffectReconciliation(mutation)
	if err != nil || !applied || settled.Status != domain.ToolEffectFailed {
		t.Fatalf("commit: effect=%#v applied=%t err=%v", settled, applied, err)
	}
	if duplicate, _, applied, err := fileStore.CommitToolEffectReconciliation(mutation); err != nil || applied || duplicate.Version != settled.Version {
		t.Fatalf("duplicate: effect=%#v applied=%t err=%v", duplicate, applied, err)
	}

	missing := mutation
	missing.IdempotencyKey = "missing"
	if _, _, _, err := fileStore.CommitToolEffectReconciliation(missing); !IsNotFound(err) {
		t.Fatalf("duplicate event with missing effect: %v", err)
	}
	missing.Event.ID = "event-missing"
	if _, _, _, err := fileStore.CommitToolEffectReconciliation(missing); !IsNotFound(err) {
		t.Fatalf("missing effect: %v", err)
	}
}

func TestFileStoreToolEffectReconciliationRollsBackFailedSave(t *testing.T) {
	directory := t.TempDir()
	fileStore, run := checkpointFileTestRunAtPath(t, filepath.Join(directory, "agentflow.json"))
	effect := beginUncertainEffect(t, fileStore, run, "effect-rollback")
	if err := os.RemoveAll(directory); err != nil {
		t.Fatal(err)
	}
	mutation := reconciliationMutation(effect, domain.ToolEffectConfirmFailed, domain.ToolEffectFailed, nil)
	if _, _, _, err := fileStore.CommitToolEffectReconciliation(mutation); err == nil {
		t.Fatal("expected persistence failure")
	}
	records, _ := fileStore.ListToolEffects(run.ID)
	events, _ := fileStore.ListRunEvents(run.ID)
	if len(records) != 1 || records[0].Status != domain.ToolEffectNeedsReconciliation || len(events) != 0 {
		t.Fatalf("failed save was not rolled back: effects=%#v events=%#v", records, events)
	}
}

func beginUncertainEffect(t *testing.T, fileStore *FileStore, run domain.Run, key string) domain.ToolEffectRecord {
	t.Helper()
	effect, execute, err := fileStore.BeginToolEffect(domain.ToolEffectRecord{
		IdempotencyKey: key, RunID: run.ID, StageID: "stage-1", ToolCallID: "call-1",
		ToolName: "writer", RequestHash: "request",
	})
	if err != nil || !execute {
		t.Fatalf("begin effect: %#v execute=%t err=%v", effect, execute, err)
	}
	effect, err = fileStore.MarkToolEffectNeedsReconciliation(key, "timeout")
	if err != nil {
		t.Fatal(err)
	}
	return effect
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
