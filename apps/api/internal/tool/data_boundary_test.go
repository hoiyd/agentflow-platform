package tool

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"agentflow-platform/apps/api/internal/tool/policy"
)

func TestPrivateReadBlocksNetworkRegardlessOfBatchOrder(t *testing.T) {
	for _, batch := range []string{"sequential", "read-first", "network-first"} {
		t.Run(batch, func(t *testing.T) {
			client := tavilyTestClient(t)
			var networkCalls atomic.Int32
			client.http.Transport = tavilyRoundTripFunc(func(*http.Request) (*http.Response, error) {
				networkCalls.Add(1)
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"results":[]}`))}, nil
			})
			read := Binding{Descriptor: Descriptor{
				Name: "private_read_fixture", Parameters: ObjectSchema(nil, nil), Concurrency: ConcurrencyPolicy{Mode: ConcurrencyReadOnly},
				Security: policy.NormalizeCapability(policy.Capability{Scope: policy.Scope{Resources: []policy.ResourceScope{{Kind: policy.ResourceConversation, Name: "private_fixture", Access: policy.AccessRead}}}}),
			}, Handler: func(context.Context, json.RawMessage) (any, error) { return "fixture-private-data", nil }}
			search := WebSearchTool(client)
			security := policyFor("web_search", policy.ActionAllow, search.Descriptor.Security)
			security.Rules = append(security.Rules, policy.Rule{ID: "read-fixture", Tool: read.Descriptor.Name, Action: policy.ActionAllow, Capability: read.Descriptor.Security})
			catalog, err := NewCatalogWithPolicy(security, read, search)
			if err != nil {
				t.Fatal(err)
			}
			executor := NewExecutor(catalog, ExecutorOptions{CredentialScopes: []string{TavilyCredentialScope}})
			local := ExecutionRequest{Tool: read.Descriptor.Name, Arguments: json.RawMessage(`{}`)}
			external := ExecutionRequest{Tool: "web_search", Arguments: json.RawMessage(`{"query":"encoded-or-paraphrased-private-content"}`)}
			var denied ExecutionResult
			switch batch {
			case "sequential":
				if result := executor.Execute(t.Context(), local); result.Error != nil {
					t.Fatal(result.Error)
				}
				denied = executor.Execute(t.Context(), external)
			case "read-first":
				results := executor.ExecuteBatch(t.Context(), []ExecutionRequest{local, external})
				if results[0].Error != nil {
					t.Fatal(results[0].Error)
				}
				denied = results[1]
			case "network-first":
				results := executor.ExecuteBatch(t.Context(), []ExecutionRequest{external, local})
				if results[1].Error != nil {
					t.Fatal(results[1].Error)
				}
				denied = results[0]
			}
			if denied.Error == nil || denied.Error.Code != ErrorSecurityPolicyDenied || denied.PolicyDecision == nil || denied.PolicyDecision.Reason != "private_context_egress_denied" || networkCalls.Load() != 0 {
				t.Fatalf("private read escaped the boundary: network=%d result=%+v", networkCalls.Load(), denied)
			}
		})
	}
}
