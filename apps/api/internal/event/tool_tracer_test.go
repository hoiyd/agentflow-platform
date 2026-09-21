package event

import "agentflow-platform/apps/api/internal/testsupport/fixturestore"

import (
	"context"
	"encoding/json"

	"strings"
	"testing"

	"agentflow-platform/apps/api/internal/domain"

	"agentflow-platform/apps/api/internal/tool"
	"agentflow-platform/apps/api/internal/tool/policy"
	"agentflow-platform/apps/api/internal/tool/progress"
)

func TestToolExecutionTracerRecordsCanceledExecution(t *testing.T) {
	fixtureStore := fixturestore.New()

	conversation, err := fixtureStore.CreateConversation("tool trace")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	run, err := fixtureStore.CreateRunWithContract("agent_planner", conversation.ID, domain.RuntimeSnapshot{
		SchemaVersion: domain.CurrentRuntimeSnapshotVersion, RunBudget: &domain.RuntimeRunBudget{},
	}, nil)

	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	catalog, err := tool.NewCatalog(tool.Binding{
		Descriptor: tool.Descriptor{Name: "blocking", Parameters: tool.ObjectSchema(nil, nil)},
		Handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	})
	if err != nil {
		t.Fatalf("new catalog: %v", err)
	}
	executor := tool.NewExecutor(catalog, tool.ExecutorOptions{
		Tracer: NewToolExecutionTracer(NewRecorder(fixtureStore), run.ID, ""),
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result := executor.Execute(ctx, tool.ExecutionRequest{CallID: "call-1", Tool: "blocking"})
	if result.Error == nil || result.Error.Code != tool.ErrorExecutionCanceled {
		t.Fatalf("expected cancellation error, got %#v", result.Error)
	}

	events, err := fixtureStore.ListRunEvents(run.ID)
	if err != nil {
		t.Fatalf("list run events: %v", err)
	}
	if len(events) != 3 || events[0].Type != domain.EventToolStarted || events[1].Type != domain.EventToolPolicyEvaluated || events[2].Type != domain.EventToolFailed {
		t.Fatalf("expected tool started and failed events, got %#v", events)
	}
	if events[1].Payload["allowed"] != true || events[1].Payload["policy_version"] == "" {
		t.Fatalf("Tool policy decision is missing: %#v", events[1].Payload)
	}
	if events[2].Payload["error_code"] != string(tool.ErrorExecutionCanceled) {
		t.Fatalf("unexpected error code payload: %#v", events[2].Payload)
	}
	if events[2].Payload["error_kind"] != string(tool.ErrorExecutionCanceled) ||
		events[2].Payload["error_source"] != "tool" || events[2].Payload["error_category"] != "canceled" {
		t.Fatalf("structured failure fields are missing: %#v", events[2].Payload)
	}
	if events[0].Payload["arguments_hash"] == "" || events[0].Payload["definition_revision"] == "" {
		t.Fatalf("tool contract identity is missing from trace: %#v", events[0].Payload)
	}
}

func TestToolPolicyTracePersistsDecisionWithoutSensitiveScopeNames(t *testing.T) {
	fixtureStore := fixturestore.New()

	conversation, _ := fixtureStore.CreateConversation("policy trace")
	run, err := fixtureStore.CreateRunWithContract("agent_planner", conversation.ID, domain.RuntimeSnapshot{
		SchemaVersion: domain.CurrentRuntimeSnapshotVersion, RunBudget: &domain.RuntimeRunBudget{},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	tracer := NewToolExecutionTracer(NewRecorder(fixtureStore), run.ID, "stage-1")
	capability := policy.NormalizeCapability(policy.Capability{Scope: policy.Scope{
		Resources:   []policy.ResourceScope{{Kind: policy.ResourceWorkspace, Name: "private-customer-records", Access: policy.AccessRead}},
		Network:     policy.NetworkScope{Mode: policy.NetworkExternal, Targets: []string{"secret.internal.example"}},
		Credentials: []string{"production-api-key"},
	}})
	err = tracer.ToolPolicyEvaluated(context.Background(), tool.ExecutionRequest{CallID: "call-1", Tool: "reader"}, policy.Decision{
		Action: policy.ActionDeny, PolicyVersion: "operator-v4", RuleID: "reader-rule",
		Reason: "credential_scope_unavailable", Capability: capability,
	})
	if err != nil {
		t.Fatal(err)
	}
	events, err := fixtureStore.ListRunEvents(run.ID)
	if err != nil || len(events) != 1 {
		t.Fatalf("policy events: %#v err=%v", events, err)
	}
	encoded, _ := json.Marshal(events[0].Payload)
	for _, sensitive := range []string{"private-customer-records", "secret.internal.example", "production-api-key"} {
		if strings.Contains(string(encoded), sensitive) {
			t.Fatalf("policy event leaked %q: %s", sensitive, encoded)
		}
	}
	if events[0].Payload["credential_scope_count"] != float64(1) || events[0].Payload["network_target_count"] != float64(1) {
		t.Fatalf("policy event lost bounded metadata: %#v", events[0].Payload)
	}
}

func TestToolProgressTracePersistsEscalationsAndTerminalRecoveryMetadata(t *testing.T) {
	fixtureStore := fixturestore.New()

	conversation, _ := fixtureStore.CreateConversation("progress trace")
	run, err := fixtureStore.CreateRunWithContract("agent_planner", conversation.ID, domain.RuntimeSnapshot{
		SchemaVersion: domain.CurrentRuntimeSnapshotVersion, RunBudget: &domain.RuntimeRunBudget{},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	tracer := NewToolExecutionTracer(NewRecorder(fixtureStore), run.ID, "stage-1")
	ctx := WithScope(context.Background(), Scope{RunID: run.ID, ConversationID: conversation.ID, StageID: "stage-1", TurnID: "turn-1"})
	request := tool.ExecutionRequest{CallID: "call-1", Tool: "reader", TurnID: "turn-1"}
	base := progress.Decision{
		Version: progress.CurrentVersion, Rule: progress.RuleRepeatedFailure,
		Count: 2, Reason: "bounded reason", SignatureHash: strings.Repeat("a", 64),
		OutcomeFingerprint: strings.Repeat("b", 64), Trackable: true, Executed: true,
	}
	for _, action := range []progress.Action{progress.ActionWarn, progress.ActionBlockCall, progress.ActionHaltTurn} {
		decision := base
		decision.Action = action
		tracer.ToolProgressEvaluated(ctx, request, decision)
	}
	tracer.ToolStarted(ctx, request)
	terminal := base
	terminal.Action = progress.ActionBlockCall
	terminal.Executed = false
	tracer.ToolFinished(ctx, tool.ExecutionResult{
		CallID: request.CallID, Tool: request.Tool,
		Error:            &tool.ExecutionError{Code: tool.ErrorProgressBlocked, Message: "blocked"},
		ProgressDecision: &terminal,
	})

	events, err := fixtureStore.ListRunEvents(run.ID)
	if err != nil || len(events) != 5 {
		t.Fatalf("progress events=%#v err=%v", events, err)
	}
	want := []domain.RunEventType{
		domain.EventToolGuardWarned, domain.EventToolGuardBlocked, domain.EventTurnNoProgress,
		domain.EventToolStarted, domain.EventToolFailed,
	}
	for index, eventType := range want {
		if events[index].Type != eventType {
			t.Fatalf("event %d=%s, want %s", index, events[index].Type, eventType)
		}
	}
	if events[4].Payload["progress_guard_action"] != string(progress.ActionBlockCall) ||
		events[4].Payload["progress_guard_signature"] != terminal.SignatureHash ||
		events[4].Payload["progress_guard_executed"] != false {
		t.Fatalf("terminal recovery metadata missing: %#v", events[4].Payload)
	}
}

func TestToolExecutionTracerLinksPersistedArtifact(t *testing.T) {
	fixtureStore := fixturestore.New()

	conversation, _ := fixtureStore.CreateConversation("artifact trace")
	run, err := fixtureStore.CreateRunWithContract("agent_planner", conversation.ID, domain.RuntimeSnapshot{
		SchemaVersion: domain.CurrentRuntimeSnapshotVersion, RunBudget: &domain.RuntimeRunBudget{},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := tool.NewCatalog(tool.Binding{
		Descriptor: tool.Descriptor{Name: "future_tool", Parameters: tool.ObjectSchema(nil, nil)},
		Handler: func(context.Context, json.RawMessage) (any, error) {
			return strings.Repeat("x", 4096), nil
		},
		Policy: tool.ExecutionPolicy{MaxResultBytes: 128},
	})
	if err != nil {
		t.Fatal(err)
	}
	result := tool.NewExecutor(catalog, tool.ExecutorOptions{
		ArtifactStore: fixtureStore,
		Tracer:        NewToolExecutionTracer(NewRecorder(fixtureStore), run.ID, "stage-1"),
	}).Execute(context.Background(), tool.ExecutionRequest{
		RunID: run.ID, StageID: "stage-1", CallID: "call-1", Tool: "future_tool",
	})
	if result.Artifact == nil {
		t.Fatalf("artifact result missing: %#v", result)
	}
	events, err := fixtureStore.ListRunEvents(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 4 || events[3].Type != domain.EventToolResultPersisted || events[3].Payload["artifact_id"] != result.Artifact.ID {
		t.Fatalf("artifact event relationship missing: %#v", events)
	}
}
