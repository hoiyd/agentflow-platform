package toolloop

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"agentflow-platform/apps/api/internal/clarification"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/inference/provider"
	"agentflow-platform/apps/api/internal/tool"
)

func TestClarificationBatchNeverPartiallyExecutes(t *testing.T) {
	calls := []provider.ToolCall{{ID: "question", Type: "function", Function: provider.FunctionCall{Name: clarification.ToolName, Arguments: `{"questions":[{"id":"goal","question":"What outcome?"}]}`}}}
	for _, scope := range []string{"mixed", "isolated", "persistence_failure"} {
		t.Run(scope, func(t *testing.T) {
			writes := 0
			writer := tool.Binding{Descriptor: tool.Descriptor{Name: "fixture_action", Parameters: tool.ObjectSchema(nil, nil)}, Handler: func(context.Context, json.RawMessage) (any, error) { writes++; return "done", nil }}
			catalog, err := tool.NewCatalog(clarification.Binding(), writer)
			if err != nil {
				t.Fatal(err)
			}
			selected := append([]provider.ToolCall{}, calls...)
			if scope == "mixed" {
				selected = append(selected, provider.ToolCall{ID: "write", Type: "function", Function: provider.FunctionCall{Name: "fixture_action", Arguments: "{}"}})
			}
			request := Request{Latest: "task", Catalog: catalog}
			paused := false
			if scope != "isolated" {
				request.SuspendClarification = func(context.Context, []domain.ClarificationQuestion, Continuation) error {
					paused = true
					return errors.New("controlled storage failure")
				}
			}
			model := &modelStub{selected: provider.ChatChoice{ToolCalls: selected}}
			events, errs := Stream(boundedContext(t), model, request)
			for range events {
			}
			if err := <-errs; err == nil || errors.Is(err, clarification.Pending) {
				t.Fatalf("failure suppressed: %v", err)
			}
			if writes != 0 || paused != (scope == "persistence_failure") {
				t.Fatalf("partial batch: writes=%d pause=%t", writes, paused)
			}
		})
	}
}

func TestClarificationCheckpointPreservesEmptyReasoningAndSources(t *testing.T) {
	empty := ""
	original := Continuation{Identity: "slot-identity", NextRound: 2, PendingCallID: "call-question", Messages: []provider.Message{{Role: "user", Content: "Task", Source: "current_input", ReferenceID: "message-1"}, {Role: "assistant", ReasoningContent: &empty, ToolCalls: []provider.ToolCall{{ID: "call-question", Type: "function", Function: provider.FunctionCall{Name: clarification.ToolName, Arguments: "{}"}}}}}}
	request := Request{SuspendClarification: func(_ context.Context, _ []domain.ClarificationQuestion, c Continuation) error {
		data, err := json.Marshal(c)
		if err != nil {
			return err
		}
		var restored Continuation
		if err = json.Unmarshal(data, &restored); err != nil {
			return err
		}
		if err = restored.RestoreSources(); err != nil {
			return err
		}
		if restored.Messages[1].ReasoningContent == nil || *restored.Messages[1].ReasoningContent != "" || restored.Messages[0].ReferenceID != "message-1" {
			t.Fatal("protocol/provenance lost")
		}
		restored.PendingCallID = "wrong"
		if restored.RestoreSources() == nil {
			t.Fatal("wrong pending identity accepted")
		}
		return nil
	}}
	if err := suspend(t.Context(), request, `{"questions":[{"id":"goal","question":"What outcome?"}]}`, original); !errors.Is(err, clarification.Pending) {
		t.Fatal(err)
	}
	if err := suspend(t.Context(), request, "invalid", original); err == nil {
		t.Fatal("invalid proposal accepted")
	}
	if err := suspend(t.Context(), request, `{"questions":[]}`, original); err == nil {
		t.Fatal("empty proposal accepted")
	}
}
