package toolloop

import (
	"context"
	"strings"
	"testing"

	"agentflow-platform/apps/api/internal/inference/provider"
	"agentflow-platform/apps/api/internal/tool"
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
	return m.selected, nil
}

func (m *modelStub) PrepareFollowup(_ context.Context, messages []provider.Message) (provider.PreparedChat, error) {
	m.steps = append(m.steps, "followup")
	m.final = append([]provider.Message(nil), messages...)
	return provider.PreparedChat{RawMessages: messages}, nil
}

func (m *modelStub) StreamAnswer(_ context.Context, _ provider.PreparedChat, kind provider.ChatStreamKind, _ provider.ChatTrace, events chan<- provider.StreamEvent) (bool, error) {
	m.steps = append(m.steps, string(kind))
	events <- provider.StreamEvent{Type: "delta", Delta: "2"}
	return true, nil
}

func TestToolLoopOwnsOneToolBatchAndFollowup(t *testing.T) {
	model := &modelStub{selected: provider.ChatChoice{ToolCalls: []provider.ToolCall{{ID: "call-1", Type: "function", Function: provider.FunctionCall{Name: "calculator", Arguments: `{"expression":"1 + 1"}`}}}}}
	events, errs := Stream(context.Background(), model, Request{Latest: "calculate 1 + 1", Catalog: tool.DefaultCatalog()})
	var output string
	for event := range events {
		if event.Type == "delta" {
			output += event.Delta
		}
	}
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
	if output != "2" || len(model.final) != 3 || model.final[1].Role != "assistant" || model.final[2].Role != "tool" || model.final[2].ToolCallID != "call-1" {
		t.Fatalf("tool exchange was not returned to the model: output=%q messages=%#v", output, model.final)
	}
	if len(model.steps) != 4 || model.steps[0] != "prepare" || model.steps[1] != "select" || model.steps[2] != "followup" || model.steps[3] != string(provider.ChatStreamToolResult) {
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
	events, errs := Stream(context.Background(), model, Request{Latest: "try missing tool", Catalog: tool.DefaultCatalog()})
	for range events {
	}
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
	if len(model.final) != 3 || model.final[2].Role != "tool" || model.final[2].ToolCallID != "missing-1" || !strings.Contains(model.final[2].Content, "error") {
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
