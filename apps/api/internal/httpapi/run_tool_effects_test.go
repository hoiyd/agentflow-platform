package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/toolreconciliation"
	toolpkg "agentflow-platform/apps/api/internal/tools"
)

func TestToolEffectReconciliationHTTPFlow(t *testing.T) {
	fileStore, run := createHTTPTestRun(t)
	effect, execute, err := fileStore.BeginToolEffect(domain.ToolEffectRecord{
		IdempotencyKey: "effect-http", RunID: run.ID, StageID: "stage-1", TurnID: "turn-1",
		ToolCallID: "call-1", ToolName: "external_writer", DefinitionRevision: "revision-1", RequestHash: "request",
	})
	if err != nil || !execute {
		t.Fatalf("begin effect: effect=%#v execute=%v err=%v", effect, execute, err)
	}
	effect, err = fileStore.MarkToolEffectNeedsReconciliation(effect.IdempotencyKey, "timeout")
	if err != nil {
		t.Fatal(err)
	}
	handler := &Handler{store: fileStore, tools: toolManagerForEffectTest(t)}

	listRequest := httptest.NewRequest(http.MethodGet, "/api/runs/"+run.ID+"/tool-effects?status=needs_reconciliation&tool=external_writer", nil)
	listRequest.SetPathValue("id", run.ID)
	listResponse := httptest.NewRecorder()
	handler.listToolEffects(listResponse, listRequest)
	if listResponse.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", listResponse.Code, listResponse.Body.String())
	}
	var listed struct {
		Effects []struct {
			Status           domain.ToolEffectStatus                 `json:"status"`
			Version          int64                                   `json:"version"`
			AvailableActions []domain.ToolEffectReconciliationAction `json:"available_actions"`
		} `json:"effects"`
	}
	if err := json.Unmarshal(listResponse.Body.Bytes(), &listed); err != nil || len(listed.Effects) != 1 || listed.Effects[0].Version != effect.Version || len(listed.Effects[0].AvailableActions) != 2 {
		t.Fatalf("unexpected effect list: %#v err=%v", listed, err)
	}

	body := []byte(`{"command_id":"operator-command-1","action":"confirm_failed","expected_version":2,"actor":"operator@example.com","reason":"provider shows no matching write"}`)
	reconcileRequest := httptest.NewRequest(http.MethodPost, "/api/runs/"+run.ID+"/tool-effects/"+effect.IdempotencyKey+"/reconcile", bytes.NewReader(body))
	reconcileRequest.SetPathValue("id", run.ID)
	reconcileRequest.SetPathValue("idempotency_key", effect.IdempotencyKey)
	reconcileResponse := httptest.NewRecorder()
	handler.reconcileToolEffect(reconcileResponse, reconcileRequest)
	if reconcileResponse.Code != http.StatusOK {
		t.Fatalf("reconcile status=%d body=%s", reconcileResponse.Code, reconcileResponse.Body.String())
	}
	var outcome struct {
		Applied bool `json:"applied"`
		Effect  struct {
			Status  domain.ToolEffectStatus `json:"status"`
			Version int64                   `json:"version"`
		} `json:"effect"`
	}
	if err := json.Unmarshal(reconcileResponse.Body.Bytes(), &outcome); err != nil || !outcome.Applied || outcome.Effect.Status != domain.ToolEffectFailed || outcome.Effect.Version != 3 {
		t.Fatalf("unexpected reconciliation outcome: %#v err=%v", outcome, err)
	}
}

func TestToolEffectReconciliationHTTPRejectsInvalidRequests(t *testing.T) {
	fileStore, run := createHTTPTestRun(t)
	handler := &Handler{store: fileStore, tools: toolManagerForEffectTest(t)}

	invalidFilter := httptest.NewRequest(http.MethodGet, "/api/runs/"+run.ID+"/tool-effects?status=unknown", nil)
	invalidFilter.SetPathValue("id", run.ID)
	response := httptest.NewRecorder()
	handler.listToolEffects(response, invalidFilter)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid filter status=%d body=%s", response.Code, response.Body.String())
	}

	missingRun := httptest.NewRequest(http.MethodGet, "/api/runs/missing/tool-effects", nil)
	missingRun.SetPathValue("id", "missing")
	response = httptest.NewRecorder()
	handler.listToolEffects(response, missingRun)
	if response.Code != http.StatusNotFound {
		t.Fatalf("missing run status=%d body=%s", response.Code, response.Body.String())
	}

	unknownEffect := httptest.NewRequest(http.MethodPost, "/api/runs/"+run.ID+"/tool-effects/missing/reconcile", bytes.NewBufferString(`{"command_id":"command","action":"confirm_failed","expected_version":1,"actor":"operator","reason":"checked"}`))
	unknownEffect.SetPathValue("id", run.ID)
	unknownEffect.SetPathValue("idempotency_key", "missing")
	response = httptest.NewRecorder()
	handler.reconcileToolEffect(response, unknownEffect)
	if response.Code != http.StatusNotFound {
		t.Fatalf("missing effect status=%d body=%s", response.Code, response.Body.String())
	}

	emptyRun := httptest.NewRequest(http.MethodGet, "/api/runs//tool-effects", nil)
	response = httptest.NewRecorder()
	handler.listToolEffects(response, emptyRun)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("empty run status=%d", response.Code)
	}

	largeFilter := httptest.NewRequest(http.MethodGet, "/api/runs/"+run.ID+"/tool-effects?tool="+strings.Repeat("x", 129), nil)
	largeFilter.SetPathValue("id", run.ID)
	response = httptest.NewRecorder()
	handler.listToolEffects(response, largeFilter)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("large filter status=%d", response.Code)
	}

	for _, body := range []string{"{", `{} {}`} {
		request := httptest.NewRequest(http.MethodPost, "/api/runs/"+run.ID+"/tool-effects/effect/reconcile", strings.NewReader(body))
		request.SetPathValue("id", run.ID)
		request.SetPathValue("idempotency_key", "effect")
		response = httptest.NewRecorder()
		handler.reconcileToolEffect(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("invalid body %q status=%d", body, response.Code)
		}
	}

	invalidPath := httptest.NewRequest(http.MethodPost, "/api/runs//tool-effects//reconcile", strings.NewReader(`{}`))
	response = httptest.NewRecorder()
	handler.reconcileToolEffect(response, invalidPath)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid path status=%d", response.Code)
	}

	missingRunReconcile := httptest.NewRequest(http.MethodPost, "/api/runs/missing/tool-effects/effect/reconcile", strings.NewReader(`{"command_id":"command","action":"confirm_failed","expected_version":1,"actor":"operator","reason":"checked"}`))
	missingRunReconcile.SetPathValue("id", "missing")
	missingRunReconcile.SetPathValue("idempotency_key", "effect")
	response = httptest.NewRecorder()
	handler.reconcileToolEffect(response, missingRunReconcile)
	if response.Code != http.StatusNotFound {
		t.Fatalf("missing run reconcile status=%d", response.Code)
	}
}

func TestToolEffectClaimHTTPAndReplayContract(t *testing.T) {
	fileStore, run := createHTTPTestRun(t)
	effect, _, err := fileStore.BeginToolEffect(domain.ToolEffectRecord{
		IdempotencyKey: "claimed-http", RunID: run.ID, StageID: "stage-1", ToolCallID: "call-1",
		ToolName: "external_writer", DefinitionRevision: "revision-1", RequestHash: "request",
	})
	if err != nil {
		t.Fatal(err)
	}
	effect, err = fileStore.MarkToolEffectNeedsReconciliation(effect.IdempotencyKey, "uncertain")
	if err != nil {
		t.Fatal(err)
	}
	claimEvent, err := event.NewRunEvent(domain.EventToolEffectReconciliationStarted, event.EventMetadata{
		RunID: run.ID, ConversationID: run.ConversationID, StageID: effect.StageID,
	}, event.ToolEffectReconciliationPayload{
		CommandID: "claim", CommandHash: "hash", IdempotencyKey: effect.IdempotencyKey, Action: string(domain.ToolEffectRetrySameKey),
		ExpectedVersion: effect.Version, Outcome: "pending", Status: string(domain.ToolEffectReconciling),
	})
	if err != nil {
		t.Fatal(err)
	}
	claimEvent.ID = "claim-http-event"
	if _, _, _, err := fileStore.CommitToolEffectReconciliation(domain.ToolEffectReconciliation{
		CommandID: "claim", IdempotencyKey: effect.IdempotencyKey, ExpectedVersion: effect.Version,
		Action: domain.ToolEffectRetrySameKey, NextStatus: domain.ToolEffectReconciling, Event: claimEvent,
	}); err != nil {
		t.Fatal(err)
	}
	handler := &Handler{store: fileStore, tools: toolManagerForEffectTest(t)}
	request := httptest.NewRequest(http.MethodGet, "/api/runs/"+run.ID+"/tool-effects?status=reconciling", nil)
	request.SetPathValue("id", run.ID)
	response := httptest.NewRecorder()
	handler.listToolEffects(response, request)
	var body struct {
		Effects []toolreconciliation.ToolEffectView `json:"effects"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &body) != nil || len(body.Effects) != 1 || body.Effects[0].Status != domain.ToolEffectReconciling || len(body.Effects[0].AvailableActions) != 2 {
		t.Fatalf("claim list contract: %d %s", response.Code, response.Body.String())
	}
	replay, ok, err := fileStore.GetRunReplay(run.ID)
	if err != nil || !ok || len(replay.ToolEffects) != 1 || replay.ToolEffects[0].Status != domain.ToolEffectReconciling {
		t.Fatalf("replay claim: %#v %v", replay.ToolEffects, err)
	}
}

func TestWriteToolEffectFailureMapsStatus(t *testing.T) {
	tests := []struct {
		err    error
		status int
	}{
		{store.ErrNotFound("effect"), http.StatusNotFound},
		{&store.ToolEffectVersionConflict{Expected: 1, Actual: 2}, http.StatusConflict},
		{&toolreconciliation.ReconciliationError{Code: toolreconciliation.ReconciliationNotFound, Message: "missing"}, http.StatusNotFound},
		{&toolreconciliation.ReconciliationError{Code: toolreconciliation.ReconciliationUnavailable, Message: "unavailable"}, http.StatusConflict},
		{&toolreconciliation.ReconciliationError{Code: toolreconciliation.ReconciliationInvalid, Message: "invalid"}, http.StatusBadRequest},
		{errors.New("failed"), http.StatusInternalServerError},
	}
	for _, test := range tests {
		response := httptest.NewRecorder()
		writeToolEffectFailure(response, httptest.NewRequest(http.MethodPost, "/", nil), test.err)
		if response.Code != test.status {
			t.Fatalf("error %v status=%d want=%d", test.err, response.Code, test.status)
		}
	}
}

func toolManagerForEffectTest(t *testing.T) *toolpkg.Manager {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tools.json")
	if err := toolpkg.SaveConfig(path, toolpkg.DefaultConfig()); err != nil {
		t.Fatal(err)
	}
	manager, err := toolpkg.NewManager(path)
	if err != nil {
		t.Fatal(err)
	}
	return manager
}
