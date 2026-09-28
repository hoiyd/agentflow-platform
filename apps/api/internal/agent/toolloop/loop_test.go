package toolloop

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/inference/provider"
	"agentflow-platform/apps/api/internal/tool"
	"agentflow-platform/apps/api/internal/tool/policy"
)

type modelStub struct {
	selected provider.ChatChoice
	steps    []string
	final    []provider.Message
}

func (m *modelStub) HasAPIKey() bool { return true }

func (m *modelStub) PrepareAgentChat(_ context.Context, request provider.ChatRequest) (provider.PreparedChat, error) {
	m.steps = append(m.steps, "prepare")
	return provider.PreparedChat{RawMessages: []provider.Message{{Role: "user", Content: request.Latest}}}, nil
}

func (m *modelStub) SelectTools(_ context.Context, _ provider.PreparedChat, _ []map[string]any, _ provider.ChatTrace) (provider.ChatChoice, error) {
	m.steps = append(m.steps, "select")
	if m.final != nil {
		return provider.ChatChoice{Content: "2"}, nil
	}
	return m.selected, nil
}

func (m *modelStub) PrepareFollowup(_ context.Context, messages []provider.Message, definitions []map[string]any) (provider.PreparedChat, error) {
	m.steps = append(m.steps, "followup")
	m.final = append([]provider.Message(nil), messages...)
	return provider.PreparedChat{RawMessages: messages}, nil
}

func boundedContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	t.Cleanup(cancel)
	return ctx
}

func (m *modelStub) StreamAnswer(_ context.Context, _ provider.PreparedChat, kind provider.ChatStreamKind, _ provider.ChatTrace, events chan<- provider.StreamEvent) (bool, error) {
	m.steps = append(m.steps, string(kind))
	events <- provider.StreamEvent{Type: "delta", Delta: "2"}
	return true, nil
}

func TestToolLoopOwnsToolBatchAndNextDecision(t *testing.T) {
	model := &modelStub{selected: provider.ChatChoice{ToolCalls: []provider.ToolCall{{ID: "call-1", Type: "function", Function: provider.FunctionCall{Name: "calculator", Arguments: `{"expression":"1 + 1"}`}}}}}
	events, errs := Stream(boundedContext(t), model, Request{Latest: "calculate 1 + 1", Catalog: tool.DefaultCatalog()})
	var output string
	for event := range events {
		if event.Type == "delta" {
			output += event.Delta
		}
	}
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
	if output != "2" || len(model.final) != 3 || model.final[1].Role != "assistant" || model.final[2].Role != "tool" || model.final[2].ToolCallID != model.final[1].ToolCalls[0].ID {
		t.Fatalf("tool exchange was not returned to the model: output=%q messages=%#v", output, model.final)
	}
	if len(model.steps) != 4 || model.steps[0] != "prepare" || model.steps[1] != "select" || model.steps[2] != "followup" || model.steps[3] != "select" {
		t.Fatalf("unexpected tool loop order: %#v", model.steps)
	}
}

func TestToolLoopDirectAnswerSkipsExecutor(t *testing.T) {
	model := &modelStub{selected: provider.ChatChoice{Content: "done"}}
	events, errs := Stream(context.Background(), model, Request{Latest: "hello", Catalog: tool.DefaultCatalog()})
	var output string
	for event := range events {
		output += event.Delta
	}
	if err := <-errs; err != nil || output != "done" || len(model.steps) != 2 {
		t.Fatalf("direct answer: output=%q steps=%#v err=%v", output, model.steps, err)
	}
}

func TestToolFailureIsReturnedToModel(t *testing.T) {
	model := &modelStub{selected: provider.ChatChoice{ToolCalls: []provider.ToolCall{{
		ID: "missing-1", Type: "function", Function: provider.FunctionCall{Name: "missing_tool", Arguments: "{}"},
	}}}}
	events, errs := Stream(boundedContext(t), model, Request{Latest: "try missing tool", Catalog: tool.DefaultCatalog()})
	for range events {
	}
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
	if len(model.final) != 3 || model.final[2].Role != "tool" || model.final[2].ToolCallID != model.final[1].ToolCalls[0].ID || !strings.Contains(model.final[2].Content, "error") {
		t.Fatalf("Tool failure was not returned as a bounded model message: %#v", model.final)
	}
}

func TestToolResultRedactionAndEmission(t *testing.T) {
	payload := marshalResult(tool.ExecutionResult{Tool: "test", Result: map[string]any{"api_key": "sk-abcdefgh"}})
	if strings.Contains(payload, "abcdefgh") || !strings.Contains(payload, "[REDACTED]") {
		t.Fatalf("tool result credential leaked to model payload: %s", payload)
	}
	events := make(chan provider.StreamEvent, 8)
	if err := emitText(context.Background(), "one two", events); err != nil {
		t.Fatal(err)
	}
	close(events)
	var output string
	for event := range events {
		output += event.Delta
	}
	if output != "one two" {
		t.Fatalf("emitted text = %q", output)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := emitText(ctx, "cancel me", make(chan provider.StreamEvent)); err != context.Canceled {
		t.Fatalf("expected canceled emit, got %v", err)
	}
}

func TestToolLoopGrantsOnlyTrustedCredentialScopes(t *testing.T) {
	capability := policy.NormalizeCapability(policy.Capability{Scope: policy.Scope{
		Network:     policy.NetworkScope{Mode: policy.NetworkExternal, Targets: []string{"api.tavily.com"}},
		Credentials: []string{tool.TavilyCredentialScope},
	}})
	security := policy.Policy{Version: "test-v1", DefaultAction: policy.ActionDeny, Rules: []policy.Rule{{
		ID: "web-search", Tool: "web_search", Action: policy.ActionAllow, Capability: capability,
	}}}
	calls := 0
	catalog, err := tool.NewCatalogWithPolicy(security, tool.Binding{
		Descriptor: tool.Descriptor{Name: "web_search", Parameters: tool.ObjectSchema(nil, nil), Security: capability},
		Handler: func(context.Context, json.RawMessage) (any, error) {
			calls++
			return map[string]string{"status": "ok"}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	selection := provider.ChatChoice{ToolCalls: []provider.ToolCall{{
		ID: "search-1", Type: "function", Function: provider.FunctionCall{Name: "web_search", Arguments: `{}`},
	}}}
	for _, test := range []struct {
		name   string
		scopes []string
		want   string
		calls  int
	}{
		{name: "missing", want: "credential_scope_unavailable", calls: 0},
		{name: "granted", scopes: []string{tool.TavilyCredentialScope}, want: `"status":"ok"`, calls: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			model := &modelStub{selected: selection}
			events, errs := Stream(boundedContext(t), model, Request{
				Latest: "search", Catalog: catalog,
				ExecutorOptions: tool.ExecutorOptions{CredentialScopes: test.scopes},
			})
			for range events {
			}
			if err := <-errs; err != nil {
				t.Fatal(err)
			}
			if calls != test.calls || len(model.final) != 3 || !strings.Contains(model.final[2].Content, test.want) {
				t.Fatalf("credential scope enforcement: calls=%d final=%#v", calls, model.final)
			}
		})
	}
}

func TestToolLoopRelabelsWebSourcesBeforeModelFollowup(t *testing.T) {
	catalog, err := tool.NewCatalogWithPolicy(policy.Policy{Version: "test", DefaultAction: policy.ActionAllow}, tool.Binding{
		Descriptor: tool.Descriptor{Name: "web_search", Parameters: tool.ObjectSchema(nil, nil)},
		Handler: func(context.Context, json.RawMessage) (any, error) {
			return map[string]any{"results": []any{map[string]any{
				"source_id": "W1", "title": "Current", "url": "https://example.org/current",
			}}}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	model := &modelStub{selected: provider.ChatChoice{ToolCalls: []provider.ToolCall{{
		ID: "current-call", Type: "function", Function: provider.FunctionCall{Name: "web_search", Arguments: `{}`},
	}}}}
	sum := sha256.Sum256([]byte("run-1\x00stage-1"))
	callID := fmt.Sprintf("call_%x_1_1", sum[:16])
	events, errs := Stream(boundedContext(t), model, Request{
		Latest: "search", Catalog: catalog, Trace: provider.ChatTrace{RunID: "run-1", StepID: "stage-1"}, RunEvents: func() ([]domain.RunEvent, error) {
			return []domain.RunEvent{
				{ID: "prior-event", RunID: "run-1", Sequence: 1, Type: domain.EventToolCompleted, Payload: map[string]any{"tool_name": "web_search", "tool_call_id": "prior-call", "result": map[string]any{"results": []any{map[string]any{"title": "Prior", "url": "https://example.com/prior"}}}}},
				{ID: "current-event", RunID: "run-1", Sequence: 2, Type: domain.EventToolCompleted, Payload: map[string]any{"tool_name": "web_search", "tool_call_id": callID, "result": map[string]any{"results": []any{map[string]any{"title": "Current", "url": "https://example.org/current"}}}}},
			}, nil
		},
	})
	for range events {
	}
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
	if len(model.final) != 3 || !strings.Contains(model.final[2].Content, `"source_id":"W2"`) || !strings.Contains(model.final[2].Content, "https://example.org/current") {
		t.Fatalf("model did not receive Run-scoped Web source: %#v", model.final)
	}
}
