package openai

import (
	"context"

	"agentflow-platform/apps/api/internal/budget"
	"agentflow-platform/apps/api/internal/inference/provider"
	"agentflow-platform/apps/api/internal/redaction"
)

func (c *Client) PrepareAgentChat(ctx context.Context, request provider.ChatRequest) (provider.PreparedChat, error) {
	if !c.HasAPIKey() {
		return provider.PreparedChat{}, missingCredentialError("chat.prepare")
	}
	raw := ensureCurrentInput(buildMessagesWithSystemPrompt(request.SystemPrompt, request.History, request.ToolNames), request.Latest)
	prepared, err := c.prepareModelContextForModel(ctx, c.model, raw, request.Definitions)
	if err != nil {
		return provider.PreparedChat{}, err
	}
	return provider.PreparedChat{RawMessages: raw, Messages: prepared.messages, Manifest: prepared.manifest, Latest: request.Latest, SystemPrompt: request.SystemPrompt}, nil
}

func (c *Client) PrepareFollowup(ctx context.Context, raw []provider.Message, definitions []map[string]any) (provider.PreparedChat, error) {
	if !c.HasAPIKey() {
		return provider.PreparedChat{}, missingCredentialError("chat.prepare_followup")
	}
	prepared, err := c.prepareModelContext(ctx, raw, definitions)
	if err != nil {
		return provider.PreparedChat{}, err
	}
	return provider.PreparedChat{RawMessages: raw, Messages: prepared.messages, Manifest: prepared.manifest}, nil
}

func (c *Client) StreamToolRound(ctx context.Context, prepared provider.PreparedChat, definitions []map[string]any, trace provider.ChatTrace, events chan<- provider.StreamEvent) (provider.ChatChoice, error) {
	if !c.HasAPIKey() {
		return provider.ChatChoice{}, missingCredentialError("chat.tool_selection")
	}
	startPayload := mergePayload(map[string]any{
		"model": c.model, "call_kind": "tool_selection", "messages": messagesForTrace(prepared.Messages),
		"enabled_tools": toolNames(definitions), "input_chars": messagesTextLength(prepared.Messages),
	}, contextTracePayload(prepared.Manifest))
	if len(trace.Memories) > 0 {
		startPayload["retrieved_memories"] = retrievedMemoryPayload(trace.Memories)
	}
	if len(trace.Knowledge) > 0 {
		startPayload["retrieved_chunks"] = retrievedChunkPayload(trace.Knowledge)
	}
	span := trace.Recorder.LLMStart(ctx, trace.RunID, trace.StepID, startPayload)
	decisionCtx := budget.WithOperation(ctx, prepared.Manifest.ModelCallID)
	decisionCtx = withOutputTokenLimit(decisionCtx, prepared.Manifest.OutputReserveTokens)
	decisionCtx = withRequestManifest(decisionCtx, prepared.Manifest)
	decision, err := c.streamChat(decisionCtx, prepared.Messages, definitions, events)
	if err != nil {
		if isToolCallingUnsupported(err) {
			err = toolCallingUnsupportedError(err)
		}
		payload := addModelErrorMetadata(map[string]any{
			"source": "llm", "stage": "tool_selection", "model": c.model, "error": err.Error(),
		}, err)
		if modelErr, ok := AsModelError(err); ok && modelErr.Kind == ErrorIncompleteOutput {
			partial := decision.output
			if partial != "" {
				redacted, _ := redaction.Text(partial)
				payload["partial_output_preview"] = truncateText(redacted, 600)
				payload["partial_output_chars"] = len(partial)
			}
		}
		trace.Recorder.Error(ctx, trace.RunID, trace.StepID, payload)
		return provider.ChatChoice{}, err
	}
	choice := decision.choice
	calls := choice.ToolCalls
	usage := decision.usage
	if !usage.Valid() {
		usage = estimateUsage(messagesToText(prepared.Messages), choice.Content)
	}
	endPayload := map[string]any{
		"model": c.model, "output": choice.Content, "output_chars": len(choice.Content),
	}
	if len(calls) > 0 {
		endPayload["tool_call_count"] = len(calls)
		endPayload["manifest_id"] = prepared.Manifest.ID
	}
	trace.Recorder.LLMEnd(ctx, span, tokenPayload(endPayload, usage))
	return provider.ChatChoice{Content: choice.Content, ReasoningContent: choice.ReasoningContent, ToolCalls: calls}, nil
}

func toolNames(definitions []map[string]any) []string {
	names := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		names = append(names, toolDefinitionName(definition))
	}
	return names
}

func (c *Client) StreamAnswer(ctx context.Context, prepared provider.PreparedChat, kind provider.ChatStreamKind, trace provider.ChatTrace, events chan<- provider.StreamEvent) (bool, error) {
	if !c.HasAPIKey() {
		return false, missingCredentialError("chat.stream")
	}
	startPayload := map[string]any{
		"model": c.model, "call_kind": string(kind), "messages": messagesForTrace(prepared.Messages),
		"input_chars": messagesTextLength(prepared.Messages),
	}
	if kind == provider.ChatStreamAnswer {
		if len(trace.Memories) > 0 {
			startPayload["retrieved_memories"] = retrievedMemoryPayload(trace.Memories)
		}
		if len(trace.Knowledge) > 0 {
			startPayload["retrieved_chunks"] = retrievedChunkPayload(trace.Knowledge)
		}
	}
	span := trace.Recorder.LLMStart(ctx, trace.RunID, trace.StepID, mergePayload(startPayload, contextTracePayload(prepared.Manifest)))
	streamCtx := budget.WithOperation(ctx, prepared.Manifest.ModelCallID)
	streamCtx = withOutputTokenLimit(streamCtx, prepared.Manifest.OutputReserveTokens)
	streamCtx = withRequestManifest(streamCtx, prepared.Manifest)
	emitted, output, usage, err := c.streamMessages(streamCtx, prepared.Messages, events)
	if err != nil {
		stage := string(kind)
		trace.Recorder.Error(ctx, trace.RunID, trace.StepID, addModelErrorMetadata(map[string]any{
			"source": "llm", "stage": stage, "model": c.model, "error": err.Error(),
		}, err))
		return emitted, err
	}
	if !emitted {
		return false, invalidResponseError("chat.stream", "model stream returned no content", nil)
	}
	if !usage.Valid() {
		usage = estimateUsage(messagesToText(prepared.Messages), output)
	}
	endPayload := map[string]any{"model": c.model, "output": output, "output_chars": len(output)}
	trace.Recorder.LLMEnd(ctx, span, tokenPayload(endPayload, usage))
	return emitted, nil
}
