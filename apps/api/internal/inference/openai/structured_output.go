package openai

import (
	"context"

	"agentflow-platform/apps/api/internal/contextassembly"
	"agentflow-platform/apps/api/internal/inference/provider"
)

func (c *Client) outputResponseFormat(ctx context.Context) (map[string]any, error) {
	output, ok := provider.StructuredOutputFromContext(ctx)
	if !ok || output.Mode == "local" {
		return nil, nil
	}
	if output.Mode != "strict" || c.simulated || c.structuredOutputFormat != provider.OutputFormatOpenAIJSONSchema {
		return nil, &ModelError{Kind: ErrorInvalidRequest, Operation: "chat.output", Message: "route has no declared strict JSON Schema wire format"}
	}
	return map[string]any{"type": "json_schema", "json_schema": map[string]any{"name": "run_output", "strict": true, "schema": output.Schema}}, nil
}

func outputGuidance(ctx context.Context, messages []Message) []Message {
	output, ok := provider.StructuredOutputFromContext(ctx)
	if !ok {
		return messages
	}
	index := 0
	for index < len(messages) && messages[index].Role == "system" {
		index++
	}
	copy := make([]Message, 0, len(messages)+1)
	copy = append(copy, messages[:index]...)
	copy = append(copy, Message{Role: "system", Source: contextassembly.SourceSystem, ReferenceID: "output_contract:" + output.ContractHash,
		Content: output.Guidance()})
	return append(copy, messages[index:]...)
}
