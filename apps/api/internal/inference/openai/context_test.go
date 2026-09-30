package openai

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"agentflow-platform/apps/api/internal/contextassembly"
)

func TestPrepareModelContextPreservesCompleteProtocol(t *testing.T) {
	for _, active := range []bool{false, true} {
		for _, reasoning := range []*string{nil, ptrString(""), ptrString("private continuation")} {
			ctx := context.Background()
			if active {
				ctx = contextassembly.WithSession(ctx, contextassembly.Session{Config: contextassembly.DefaultConfig()})
			}
			input := []Message{{
				Role: "assistant", Content: "partial answer", ReasoningContent: reasoning,
				Refusal: "provider refusal", Source: contextassembly.SourceToolCall, ReferenceID: "round-1",
				ToolCalls: []ToolCall{{ID: "call-1", Type: "function", Function: FunctionCall{Name: "calculator", Arguments: `{"expression":"1+1"}`}}},
			}, {Role: "tool", Content: "2", ToolCallID: "call-1", Source: contextassembly.SourceToolResult, ReferenceID: "result-1"}}
			prepared, err := (&Client{model: "fixture"}).prepareModelContext(ctx, input, nil)
			if err != nil || len(prepared.messages) < len(input) {
				t.Fatalf("active=%v: prepare failed: %v", active, err)
			}
			// Active assembly may prepend its existing trust-policy system message.
			protocol := prepared.messages[len(prepared.messages)-len(input):]
			if !reflect.DeepEqual(protocol, input) {
				t.Fatalf("active=%v reasoning=%v: protocol changed: %#v err=%v", active, reasoning, prepared.messages, err)
			}
			wire, err := json.Marshal(prepared.messages)
			if err != nil || strings.Contains(string(wire), "round-1") || strings.Contains(string(wire), "result-1") {
				t.Fatalf("metadata leaked to wire: %s err=%v", wire, err)
			}
			redacted, _ := json.Marshal(messagesForTrace(prepared.messages))
			if strings.Contains(string(redacted), "reasoning_content") || !reflect.DeepEqual(protocol, input) {
				t.Fatal("trace filtering leaked reasoning or changed outgoing protocol")
			}
			protocol[0].ToolCalls[0].Function.Arguments = "changed"
			if protocol[0].ReasoningContent != nil {
				*protocol[0].ReasoningContent = "changed"
			}
			if input[0].ToolCalls[0].Function.Arguments != `{"expression":"1+1"}` || reasoning != nil && *reasoning == "changed" {
				t.Fatal("assembly output aliases caller-owned protocol state")
			}
		}
	}
}

func TestPrepareModelContextBudgetsNonDisplayProtocol(t *testing.T) {
	config := contextassembly.DefaultConfig()
	config.ContextWindowTokens, config.OutputReserveTokens, config.SafetyMarginTokens = 1000, 10, 10
	ctx := contextassembly.WithSession(context.Background(), contextassembly.Session{Config: config})
	for _, message := range []Message{
		{Role: "assistant", Source: contextassembly.SourceToolCall, Refusal: strings.Repeat("x", 5000)},
		{Role: "assistant", Source: contextassembly.SourceToolCall, ReasoningContent: ptrString(strings.Repeat("x", 5000))},
		{Role: "assistant", Source: contextassembly.SourceToolCall, ToolCalls: []ToolCall{{ID: "c", Function: FunctionCall{Name: "test", Arguments: strings.Repeat("x", 5000)}}}},
	} {
		_, err := (&Client{model: "fixture"}).prepareModelContext(ctx, []Message{message}, nil)
		if !errors.Is(err, contextassembly.ErrInputBudgetExceeded) {
			t.Fatalf("required protocol escaped input budget: %#v err=%v", message, err)
		}
	}
}

func ptrString(value string) *string { return &value }
