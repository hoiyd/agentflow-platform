package openai

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"agentflow-platform/apps/api/internal/budget"
	"agentflow-platform/apps/api/internal/inference/provider"
	"agentflow-platform/apps/api/internal/redaction"
)

func (c *Client) PrepareAgentChat(ctx context.Context, request provider.ChatRequest) (provider.PreparedChat, error) {
	raw := ensureCurrentInput(buildMessagesWithSystemPrompt(request.SystemPrompt, request.History, request.ToolNames), request.Latest)
	model := c.model
	definitions := request.Definitions
	if !c.HasAPIKey() {
		model = "local_fallback"
		definitions = nil
	}
	prepared, err := c.prepareModelContextForModel(ctx, model, raw, definitions)
	if err != nil {
		return provider.PreparedChat{}, err
	}
	return provider.PreparedChat{RawMessages: raw, Messages: prepared.messages, Manifest: prepared.manifest, Latest: request.Latest, SystemPrompt: request.SystemPrompt}, nil
}

func (c *Client) PrepareFollowup(ctx context.Context, raw []provider.Message) (provider.PreparedChat, error) {
	prepared, err := c.prepareModelContext(ctx, raw, nil)
	if err != nil {
		return provider.PreparedChat{}, err
	}
	return provider.PreparedChat{RawMessages: raw, Messages: prepared.messages, Manifest: prepared.manifest}, nil
}

func (c *Client) SelectTools(ctx context.Context, prepared provider.PreparedChat, definitions []map[string]any, trace provider.ChatTrace) (provider.ChatChoice, error) {
	startPayload := mergePayload(map[string]any{
		"model": c.model, "call_kind": "tool_selection", "messages": prepared.Messages,
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
	decision, err := c.complete(decisionCtx, map[string]any{
		"model": c.model, "messages": prepared.Messages, "tools": definitions, "tool_choice": "auto",
	})
	if err != nil {
		if isToolCallingUnsupported(err) {
			err = toolCallingUnsupportedError(err)
		}
		payload := addModelErrorMetadata(map[string]any{
			"source": "llm", "stage": "tool_selection", "model": c.model, "error": err.Error(),
		}, err)
		if modelErr, ok := AsModelError(err); ok && modelErr.Kind == ErrorIncompleteOutput && len(decision.Choices) > 0 {
			partial := decision.Choices[0].Message.Content
			if partial != "" {
				redacted, _ := redaction.Text(partial)
				payload["partial_output_preview"] = truncateText(redacted, 600)
				payload["partial_output_chars"] = len(partial)
			}
		}
		trace.Recorder.Error(ctx, trace.RunID, trace.StepID, payload)
		return provider.ChatChoice{}, err
	}
	choice := decision.Choices[0].Message
	calls := normalizeToolCalls(choice.ToolCalls)
	usage := decision.Usage
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
	return provider.ChatChoice{Content: choice.Content, ToolCalls: calls}, nil
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
		return c.streamLocalAnswer(ctx, prepared, trace, events)
	}
	startPayload := map[string]any{
		"model": c.model, "call_kind": string(kind), "messages": prepared.Messages,
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
		if kind == provider.ChatStreamToolResult {
			stage = "final_stream"
		}
		trace.Recorder.Error(ctx, trace.RunID, trace.StepID, addModelErrorMetadata(map[string]any{
			"source": "llm", "stage": stage, "model": c.model, "error": err.Error(),
		}, err))
		return emitted, err
	}
	if !emitted && kind == provider.ChatStreamAnswer {
		return false, invalidResponseError("chat.stream", "model stream returned no content", nil)
	}
	if !usage.Valid() && kind == provider.ChatStreamToolResult {
		usage = estimateUsage(messagesToText(prepared.Messages), output)
	}
	endPayload := map[string]any{"model": c.model, "output": output, "output_chars": len(output)}
	if kind == provider.ChatStreamToolResult {
		endPayload["manifest_id"] = prepared.Manifest.ID
	}
	trace.Recorder.LLMEnd(ctx, span, tokenPayload(endPayload, usage))
	return emitted, nil
}

func (c *Client) streamLocalAnswer(ctx context.Context, prepared provider.PreparedChat, trace provider.ChatTrace, events chan<- provider.StreamEvent) (bool, error) {
	log.Printf("chat_fallback mode=local_no_api_key latest_len=%d", len(prepared.Latest))
	output := fallbackEventResponse(prepared.Latest)
	callCtx := budget.WithOperation(ctx, prepared.Manifest.ModelCallID)
	callCtx = withRequestManifest(callCtx, prepared.Manifest)
	reservation, err := beginBudgetedModelCall(callCtx, "local_fallback", estimateTokens(messagesToText(prepared.Messages)))
	if err != nil {
		return false, err
	}
	payload, err := json.Marshal(map[string]any{"model": "local_fallback", "messages": prepared.Messages, "stream": true})
	if err != nil {
		return false, err
	}
	ref, err := c.recordModelRequest(callCtx, reservation.OperationID, "local.stream", "local_fallback", payload)
	if err != nil {
		return false, err
	}
	started := time.Now()
	startPayload := mergePayload(map[string]any{
		"model": "local_fallback", "system": prepared.SystemPrompt,
		"input": prepared.Latest, "input_chars": len(prepared.Latest),
	}, contextTracePayload(prepared.Manifest))
	if len(trace.Memories) > 0 {
		startPayload["retrieved_memories"] = retrievedMemoryPayload(trace.Memories)
	}
	if len(trace.Knowledge) > 0 {
		startPayload["retrieved_chunks"] = retrievedChunkPayload(trace.Knowledge)
	}
	span := trace.Recorder.LLMStart(ctx, trace.RunID, trace.StepID, startPayload)
	c.streamText(ctx, output, 45*time.Millisecond, events)
	usage := estimateUsage(messagesToText(prepared.Messages), output)
	c.finishModelAttempt(callCtx, ref, started, time.Time{}, usage, "local", ctx.Err())
	if err := settleBudgetedModelCall(callCtx, reservation, usage); err != nil {
		return false, err
	}
	trace.Recorder.LLMEnd(ctx, span, tokenPayload(map[string]any{
		"model": "local_fallback", "output": output, "output_chars": len(output),
	}, usage))
	return true, nil
}
