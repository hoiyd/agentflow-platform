package openai

import (
	"context"
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
	request := contextassembly.Request{Model: model, Messages: messages}
	for _, definition := range definitions {
		request.Tools = append(request.Tools, contextassembly.Tool{Name: toolDefinitionName(definition), Definition: definition})
	}
	pack, err := contextassembly.Assemble(ctx, request)
	if err != nil {
		return preparedModelContext{}, err
	}
	return preparedModelContext{messages: pack.Messages, manifest: pack.Manifest}, nil
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
