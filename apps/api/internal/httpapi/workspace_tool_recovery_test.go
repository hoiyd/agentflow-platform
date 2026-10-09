package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/tool"
	"agentflow-platform/apps/api/internal/tool/policy"
)

type recoveryCatalog struct{ catalog *tool.Catalog }

func (c recoveryCatalog) Catalog() (*tool.Catalog, error) { return c.catalog, nil }

func TestWorkspaceToolConfigurationWriteFailure(t *testing.T) {
	// A failed allowlist write must remain an internal error, not masquerade as
	// a missing Tool or return a successful toggle with unchanged durable state.
	handler, storage, db, spaces, tokens := authorizationFixture(t)
	before, err := storage.EnsureWorkspaceToolConfig(spaces[0], []string{"calculator"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE FUNCTION reject_tool_config_write() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'simulated configuration write failure'; END $$;
CREATE TRIGGER reject_tool_config_write BEFORE UPDATE ON workspace_tool_config FOR EACH ROW EXECUTE FUNCTION reject_tool_config_write()`); err != nil {
		t.Fatal(err)
	}
	response := authorizedRequest(handler.Routes(), "POST", "/api/tools/calculator/disable", spaces[0], tokens[0], "")
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("write failure status=%d body=%s", response.Code, response.Body.String())
	}
	after, err := storage.EnsureWorkspaceToolConfig(spaces[0], nil)
	if err != nil || after.Revision != before.Revision || len(after.AllowedTools) != 1 || after.AllowedTools[0] != "calculator" {
		t.Fatalf("failed write changed configuration: %#v err=%v", after, err)
	}
}

func TestWorkspaceToolRevocationBlocksRecoveryCallbacks(t *testing.T) {
	storage, run := createHTTPTestRun(t)
	calls := 0
	binding := tool.Binding{
		Descriptor: tool.Descriptor{Name: "external_writer", Parameters: tool.ObjectSchema(nil, nil), Security: policy.NormalizeCapability(policy.Capability{
			Scope: policy.Scope{Resources: []policy.ResourceScope{{Kind: policy.ResourceExternal, Name: "records", Access: policy.AccessWrite}}}, SideEffect: policy.SideEffectExternalWrite, Reversibility: policy.Compensatable, Visibility: policy.VisibilityOperator, Audit: policy.AuditFull,
		}), SideEffect: tool.SideEffectPolicy{RetryWithSameKey: true, Compensate: true}},
		Handler: func(context.Context, json.RawMessage) (any, error) { return nil, nil },
		Reconciliation: tool.SideEffectReconciliation{
			RetryWithSameKey: func(context.Context, tool.EffectReconciliationContext) (any, error) { calls++; return "ok", nil },
			Compensate:       func(context.Context, tool.EffectReconciliationContext) error { calls++; return nil },
		},
	}
	catalog, err := tool.NewCatalogWithPolicy(policy.Policy{Version: policy.CurrentVersion, DefaultAction: policy.ActionDeny, Rules: []policy.Rule{{ID: "recovery", Tool: "external_writer", Action: policy.ActionAllowAndLog, Capability: binding.Descriptor.Security}}}, binding)
	if err != nil {
		t.Fatal(err)
	}
	binding, _ = catalog.Installed("external_writer")
	record, _, err := storage.BeginToolEffect(domain.ToolEffectRecord{IdempotencyKey: "workspace-recovery", RunID: run.ID, StageID: "stage-1", ToolCallID: "call-1", ToolName: "external_writer", DefinitionRevision: binding.Descriptor.DefinitionRevision, RequestHash: "request"})
	if err != nil {
		t.Fatal(err)
	}
	record, err = storage.MarkToolEffectNeedsReconciliation(record.IdempotencyKey, "uncertain")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = storage.EnsureWorkspaceToolConfig(run.WorkspaceID, []string{"external_writer"}); err != nil {
		t.Fatal(err)
	}
	if err = storage.SetWorkspaceToolEnabled(run.WorkspaceID, "external_writer", false); err != nil {
		t.Fatal(err)
	}
	handler := &Handler{store: storage, tools: recoveryCatalog{catalog}}
	for _, action := range []string{"retry_with_same_key", "compensate"} {
		request := httptest.NewRequest(http.MethodPost, "/api/runs/"+run.ID+"/tool-effects/"+record.IdempotencyKey+"/reconcile", strings.NewReader(fmt.Sprintf(`{"command_id":%q,"action":%q,"expected_version":%d,"actor":"owner","reason":"recovery"}`, action, action, record.Version)))
		request.SetPathValue("id", run.ID)
		request.SetPathValue("idempotency_key", record.IdempotencyKey)
		response := httptest.NewRecorder()
		handler.reconcileToolEffect(response, request)
		if response.Code != http.StatusConflict || calls != 0 {
			t.Fatalf("revoked callback: status=%d calls=%d body=%s", response.Code, calls, response.Body.String())
		}
	}
	records, err := storage.ListToolEffects(run.ID)
	if err != nil || records[0].Version != record.Version {
		t.Fatalf("denial changed durable state: %#v %v", records, err)
	}
}
