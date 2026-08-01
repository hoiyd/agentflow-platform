package openai

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/budget"
	"agentflow-platform/apps/api/internal/contextassembly"
	"agentflow-platform/apps/api/internal/domain"
	tracepkg "agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/tools"
)

func (c *Client) streamFallback(ctx context.Context, latest string, chunks chan<- string) {
	response := "Local fallback response: backend streaming is working. Add OPENAI_API_KEY in apps/api/.env to enable real OpenAI responses. You said: " + latest
	words := strings.Split(response, " ")
	for i, word := range words {
		select {
		case <-ctx.Done():
			return
		case chunks <- word + suffix(i, len(words)):
			time.Sleep(45 * time.Millisecond)
		}
	}
}

func fallbackCompletion(systemPrompt string, prompt string) string {
	lower := strings.ToLower(systemPrompt)
	switch {
	case strings.Contains(lower, "planner"):
		return "Plan:\n1. Clarify the user's goal and expected output.\n2. Execute the main work using the selected worker agent.\n3. Review the result for completeness, risks, and missing details.\n\nSuccess criteria:\n- The final answer directly addresses the task.\n- Key assumptions and gaps are visible."
	case strings.Contains(lower, "worker"):
		return "Worker result:\nI executed the plan using the provided task context. The result is a concise draft that addresses the requested goal and preserves any important constraints.\n\nTask context:\n" + truncateText(prompt, 600)
	case strings.Contains(lower, "reviewer"):
		return "Review:\n- The result follows the requested fixed collaboration flow.\n- Remaining risk: verify domain-specific details when the task depends on external facts."
	case strings.Contains(lower, "finalizer"):
		return "Final answer:\nThe task was processed through Planner, Worker, Reviewer, and Finalizer stages. The final response combines the plan, execution result, and review notes into one answer.\n\n" + truncateText(prompt, 800)
	default:
		return "Generated response:\n" + truncateText(prompt, 800)
	}
}

func truncateText(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "..."
}

func (c *Client) streamFallbackEvents(ctx context.Context, latest string, events chan<- StreamEvent) {
	c.streamText(ctx, fallbackEventResponse(latest), 45*time.Millisecond, events)
}

func fallbackEventResponse(latest string) string {
	return "Local fallback response: backend streaming is working. Add OPENAI_API_KEY in apps/api/.env to enable model-directed tool calling. You said: " + latest
}

func (c *Client) streamText(ctx context.Context, text string, delay time.Duration, events chan<- StreamEvent) {
	words := strings.Split(text, " ")
	for i, word := range words {
		select {
		case <-ctx.Done():
			return
		case events <- StreamEvent{Type: "delta", Delta: word + suffix(i, len(words))}:
			time.Sleep(delay)
		}
	}
}

func (c *Client) streamOpenAIWithTools(ctx context.Context, systemPrompt string, history []domain.Message, latest string, catalog *tools.Catalog, events chan<- StreamEvent, recorder *tracepkg.Recorder, runID string, stepID string, retrievedMemories []domain.RetrievedMemory, retrievedChunks []domain.RetrievedDocumentChunk) error {
	enabledTools := catalog.EnabledNames()
	definitions := catalog.Definitions()
	rawMessages := ensureCurrentInput(buildMessagesWithSystemPrompt(systemPrompt, history, enabledTools), latest)
	prepared, err := c.prepareModelContext(ctx, rawMessages, definitions)
	if err != nil {
		return err
	}
	messages := prepared.messages
	startPayload := mergePayload(map[string]any{
		"model":         c.model,
		"call_kind":     "tool_selection",
		"messages":      messages,
		"enabled_tools": enabledTools,
		"input_chars":   messagesTextLength(messages),
	}, contextTracePayload(prepared.manifest))
	if len(retrievedMemories) > 0 {
		startPayload["retrieved_memories"] = retrievedMemoryPayload(retrievedMemories)
	}
	if len(retrievedChunks) > 0 {
		startPayload["retrieved_chunks"] = retrievedChunkPayload(retrievedChunks)
	}
	llmSpan := recorder.LLMStart(ctx, runID, stepID, startPayload)
	decisionCtx := budget.WithOperation(ctx, prepared.manifest.ModelCallID)
	decisionCtx = withOutputTokenLimit(decisionCtx, prepared.manifest.OutputReserveTokens)
	decision, err := c.complete(decisionCtx, map[string]any{
		"model":       c.model,
		"messages":    messages,
		"tools":       definitions,
		"tool_choice": "auto",
		"temperature": 0.2,
	})
	if err != nil {
		if !isToolCallingUnsupported(err) {
			recorder.Error(ctx, runID, stepID, addModelErrorMetadata(map[string]any{
				"source": "llm",
				"stage":  "tool_selection",
				"model":  c.model,
				"error":  err.Error(),
			}, err))
			return err
		}
		log.Printf(
			"chat_capability_fallback capability=tool_calling reason=%q model=%s enabled_tools=%q history_messages=%d",
			err.Error(),
			c.model,
			strings.Join(enabledTools, ","),
			len(messages),
		)
		recorder.Error(ctx, runID, stepID, addModelErrorMetadata(map[string]any{
			"source": "llm", "stage": "tool_selection", "model": c.model,
			"manifest_id": prepared.manifest.ID, "error": err.Error(),
		}, err))
		prepared, err = c.prepareModelContext(ctx, rawMessages, nil)
		if err != nil {
			return err
		}
		messages = prepared.messages
		llmSpan = recorder.LLMStart(ctx, runID, stepID, mergePayload(map[string]any{
			"model": c.model, "call_kind": "text_capability_fallback", "messages": messages,
			"input_chars": messagesTextLength(messages),
		}, contextTracePayload(prepared.manifest)))
		decisionCtx = budget.WithOperation(ctx, prepared.manifest.ModelCallID)
		decisionCtx = withOutputTokenLimit(decisionCtx, prepared.manifest.OutputReserveTokens)
		decision, err = c.complete(decisionCtx, map[string]any{
			"model":       c.model,
			"messages":    messages,
			"temperature": 0.2,
		})
		if err != nil {
			recorder.Error(ctx, runID, stepID, addModelErrorMetadata(map[string]any{
				"source": "llm",
				"stage":  "text_fallback",
				"model":  c.model,
				"error":  err.Error(),
			}, err))
			return err
		}
	}

	choice := decision.Choices[0]
	toolCalls := choice.Message.ToolCalls
	if len(toolCalls) == 0 {
		if fallback, ok := parseFallbackToolCall(choice.Message.Content); ok {
			log.Printf(
				"chat_fallback mode=json_tool_call tool=%s arguments=%q content_len=%d",
				fallback.Function.Name,
				fallback.Function.Arguments,
				len(choice.Message.Content),
			)
			toolCalls = []ToolCall{fallback}
		}
	}

	if len(toolCalls) == 0 {
		err := emitText(ctx, choice.Message.Content, events)
		if err != nil {
			return err
		}
		usage := decision.Usage
		if !usage.Valid() {
			usage = estimateUsage(messagesToText(messages), choice.Message.Content)
		}
		recorder.LLMEnd(ctx, llmSpan, tokenPayload(map[string]any{
			"model":        c.model,
			"output":       choice.Message.Content,
			"output_chars": len(choice.Message.Content),
		}, usage))
		return nil
	}

	normalizedCalls := normalizeToolCalls(toolCalls)
	usage := decision.Usage
	if !usage.Valid() {
		usage = estimateUsage(messagesToText(messages), choice.Message.Content)
	}
	recorder.LLMEnd(ctx, llmSpan, tokenPayload(map[string]any{
		"model": c.model, "output": choice.Message.Content, "output_chars": len(choice.Message.Content),
		"tool_call_count": len(normalizedCalls), "manifest_id": prepared.manifest.ID,
	}, usage))

	rawMessages = append(rawMessages, Message{
		Role:        "assistant",
		Content:     choice.Message.Content,
		ToolCalls:   normalizedCalls,
		Source:      contextassembly.SourceToolCall,
		ReferenceID: "tool_calls",
	})

	toolExecutor := tools.NewExecutor(catalog, tools.ExecutorOptions{
		Tracer: &streamToolExecutionTracer{
			delegate: tracepkg.NewToolExecutionTracer(recorder, runID, stepID),
			events:   events,
		},
	})
	requests := make([]tools.ExecutionRequest, 0, len(normalizedCalls))
	for _, call := range normalizedCalls {
		requests = append(requests, tools.ExecutionRequest{
			CallID: call.ID, Tool: call.Function.Name,
			Arguments: json.RawMessage(call.Function.Arguments),
		})
	}
	results := toolExecutor.ExecuteBatch(ctx, requests)
	for _, result := range results {
		if exceeded, ok := budget.AsExceeded(result.Error); ok {
			return exceeded
		}
	}
	for index, result := range results {
		call := normalizedCalls[index]
		resultText := marshalResult(result)
		rawMessages = append(rawMessages, Message{
			Role:        "tool",
			ToolCallID:  call.ID,
			Content:     resultText,
			Source:      contextassembly.SourceToolResult,
			ReferenceID: call.ID,
		})
	}

	prepared, err = c.prepareModelContext(ctx, rawMessages, nil)
	if err != nil {
		return err
	}
	messages = prepared.messages
	llmSpan = recorder.LLMStart(ctx, runID, stepID, mergePayload(map[string]any{
		"model": c.model, "call_kind": "tool_result_response", "messages": messages,
		"input_chars": messagesTextLength(messages),
	}, contextTracePayload(prepared.manifest)))
	streamCtx := budget.WithOperation(ctx, prepared.manifest.ModelCallID)
	streamCtx = withOutputTokenLimit(streamCtx, prepared.manifest.OutputReserveTokens)
	emitted, finalOutput, finalUsage, err := c.streamMessages(streamCtx, messages, events)
	if err != nil {
		recorder.Error(ctx, runID, stepID, addModelErrorMetadata(map[string]any{
			"source": "llm",
			"stage":  "final_stream",
			"model":  c.model,
			"error":  err.Error(),
		}, err))
		return err
	}
	if !emitted {
		log.Printf("chat_fallback mode=tool_summary_no_stream tool_count=%d", len(results))
		if err := emitText(ctx, summarizeToolResults(results), events); err != nil {
			return err
		}
	}
	if !finalUsage.Valid() {
		finalUsage = estimateUsage(messagesToText(messages), finalOutput)
	}
	recorder.LLMEnd(ctx, llmSpan, tokenPayload(map[string]any{
		"model":        c.model,
		"manifest_id":  prepared.manifest.ID,
		"output":       finalOutput,
		"output_chars": len(finalOutput),
	}, finalUsage))
	return nil
}
