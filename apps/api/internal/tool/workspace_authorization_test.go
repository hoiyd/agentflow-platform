package tool

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"agentflow-platform/apps/api/internal/tool/policy"
)

func TestExecutorRechecksCurrentAuthorizationForEveryCall(t *testing.T) {
	entered := 0
	allowed := true
	catalog, err := NewCatalog(Binding{Descriptor: Descriptor{Name: "reader", Description: "Read", Parameters: ObjectSchema(nil, nil)}, Handler: func(context.Context, json.RawMessage) (any, error) { entered++; return "ok", nil }})
	if err != nil {
		t.Fatal(err)
	}
	executor := NewExecutor(catalog, ExecutorOptions{Authorize: func(context.Context, ExecutionRequest, policy.Scope) error {
		if !allowed {
			return errors.New("workspace_disabled")
		}
		return nil
	}})
	first := executor.Execute(context.Background(), ExecutionRequest{Tool: "reader"})
	if first.Error != nil {
		t.Fatal(first.Error)
	}
	allowed = false
	second := executor.Execute(context.Background(), ExecutionRequest{Tool: "reader"})
	if second.Error == nil || second.Error.Code != ErrorSecurityPolicyDenied || entered != 1 {
		t.Fatalf("revocation bypass: %#v calls=%d", second, entered)
	}
	if second.PolicyDecision == nil || second.PolicyDecision.Allowed {
		t.Fatal("missing denied policy evidence")
	}
}
