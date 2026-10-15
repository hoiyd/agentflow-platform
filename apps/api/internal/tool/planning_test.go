package tool

import (
	"context"
	"encoding/json"
	"testing"

	"agentflow-platform/apps/api/internal/budget"
	"agentflow-platform/apps/api/internal/tool/policy"
)

func TestPlanningExecutorDeniesBeforeResolverBudgetAndHandler(t *testing.T) {
	for _, capability := range []policy.Capability{
		{SideEffect: policy.SideEffectInternalWrite},
		{SideEffect: policy.SideEffectExternalWrite},
		{SideEffect: policy.SideEffectDestructive},
		{Scope: policy.Scope{Resources: []policy.ResourceScope{{Kind: policy.ResourceFilesystem, Name: "output", Access: policy.AccessWrite}}}},
		{Source: policy.SourceRemote},
	} {
		t.Run(string(capability.SideEffect)+string(capability.Source), func(t *testing.T) {
			called := false
			binding := Binding{Descriptor: Descriptor{Name: "unsafe", Parameters: ObjectSchema(nil, nil), Security: capability},
				ResolveScope: func(context.Context, json.RawMessage) (policy.Scope, error) {
					called = true
					return policy.Scope{}, nil
				},
				Handler: func(context.Context, json.RawMessage) (any, error) { called = true; return nil, nil }}
			catalog, err := NewCatalog(binding)
			if err != nil {
				t.Fatal(err)
			}
			controller := &toolBudgetController{}
			result := NewExecutor(catalog, ExecutorOptions{PlanOnly: true}).Execute(budget.WithController(t.Context(), controller), ExecutionRequest{Tool: "unsafe", Arguments: json.RawMessage(`{}`)})
			if result.Error == nil || result.Error.Code != ErrorSecurityPolicyDenied || result.PolicyDecision == nil || result.PolicyDecision.Reason != "plan_only_denied" || called || controller.calls != 0 {
				t.Fatalf("planning boundary bypassed: %#v called=%v budget=%d", result, called, controller.calls)
			}
		})
	}
	catalog, _ := NewCatalog(CalculatorTool())
	if result := NewExecutor(catalog, ExecutorOptions{PlanOnly: true}).Execute(t.Context(), ExecutionRequest{Tool: "calculator", Arguments: json.RawMessage(`{"expression":"2+3"}`)}); result.Error != nil {
		t.Fatal(result.Error)
	}
}
