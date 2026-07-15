package openai

import (
	"context"
	"encoding/json"
	"strings"

	"agentflow-platform/apps/api/internal/contextassembly"
	"agentflow-platform/apps/api/internal/domain"
)

type preparedModelContext struct {
	messages []Message
	manifest domain.ContextManifest
}

func (c *Client) prepareModelContext(ctx context.Context, messages []Message, definitions []map[string]any) (preparedModelContext, error) {
	return c.prepareModelContextForModel(ctx, c.model, messages, definitions)
}

func (c *Client) prepareModelContextForModel(ctx context.Context, model string, messages []Message, definitions []map[string]any) (preparedModelContext, error) {
	request := contextassembly.Request{Model: model, Messages: make([]contextassembly.Message, 0, len(messages))}
	for _, message := range messages {
		toolCalls, err := json.Marshal(message.ToolCalls)
		if err != nil {
			return preparedModelContext{}, err
		}
		if len(message.ToolCalls) == 0 {
			toolCalls = nil
		}
		request.Messages = append(request.Messages, contextassembly.Message{
			Source: message.Source, ReferenceID: message.ReferenceID, Role: message.Role,
			Content: message.Content, ToolCallID: message.ToolCallID, ToolCalls: toolCalls,
		})
	}
	for _, definition := range definitions {
		request.Tools = append(request.Tools, contextassembly.Tool{Name: toolDefinitionName(definition), Definition: definition})
	}
	pack, err := contextassembly.Assemble(ctx, request)
	if err != nil {
		return preparedModelContext{}, err
	}
	prepared := make([]Message, 0, len(pack.Messages))
	for _, message := range pack.Messages {
		var toolCalls []ToolCall
		if len(message.ToolCalls) > 0 {
			if err := json.Unmarshal(message.ToolCalls, &toolCalls); err != nil {
				return preparedModelContext{}, err
			}
		}
		prepared = append(prepared, Message{
			Role: message.Role, Content: message.Content, ToolCallID: message.ToolCallID,
			ToolCalls: toolCalls, Source: message.Source, ReferenceID: message.ReferenceID,
		})
	}
	return preparedModelContext{messages: prepared, manifest: pack.Manifest}, nil
}

func toolDefinitionName(definition map[string]any) string {
	function, _ := definition["function"].(map[string]any)
	name, _ := function["name"].(string)
	return strings.TrimSpace(name)
}

func textPromptParts(messages []Message) (string, string) {
	var system strings.Builder
	var prompt strings.Builder
	for _, message := range messages {
		if message.Role == "system" {
			system.WriteString(message.Content)
			continue
		}
		if prompt.Len() > 0 {
			prompt.WriteString("\n")
		}
		prompt.WriteString(message.Content)
	}
	return strings.TrimSpace(system.String()), strings.TrimSpace(prompt.String())
}

func contextTracePayload(manifest domain.ContextManifest) map[string]any {
	if manifest.ID == "" {
		return map[string]any{}
	}
	return map[string]any{
		"manifest_id": manifest.ID, "model_call_id": manifest.ModelCallID,
		"context_assembler_version": manifest.AssemblerVersion, "context_input_budget_tokens": manifest.InputBudgetTokens,
		"context_estimated_input_tokens": manifest.EstimatedInputTokens, "context_prefix_hash": manifest.PrefixHash,
	}
}

func mergePayload(target map[string]any, source map[string]any) map[string]any {
	for key, value := range source {
		target[key] = value
	}
	return target
}
