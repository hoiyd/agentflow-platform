package toolloop

import (
	"context"
	"testing"

	"agentflow-platform/apps/api/internal/inference/provider"
	"agentflow-platform/apps/api/internal/tool"
)

type modelStub struct {
	selected provider.ChatChoice
	steps    []string
	final   []provider.Message
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
