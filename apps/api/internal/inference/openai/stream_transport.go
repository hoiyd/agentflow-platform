package openai

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"log"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/budget"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/inference/provider"
	"agentflow-platform/apps/api/internal/inference/requestcontrol"
)

func (c *Client) streamMessages(ctx context.Context, messages []Message, events chan<- StreamEvent) (bool, string, Usage, error) {
	result, err := c.streamChat(ctx, messages, nil, events)
	return result.answerEmitted, result.output, result.usage, err
}

func (c *Client) streamChat(ctx context.Context, messages []Message, definitions []map[string]any, events chan<- StreamEvent) (streamAttemptResult, error) {
	if err := c.validateSampling("chat.stream"); err != nil {
		return streamAttemptResult{}, err
	}
	estimated := estimateTokens(messagesToText(messages))
	if len(definitions) > 0 {
		payload, err := json.Marshal(map[string]any{"messages": messages, "tools": definitions})
		if err != nil {
			return streamAttemptResult{}, err
		}
		estimated = estimatedRequestTokens(payload)
	}
	reservation, err := beginBudgetedModelCall(ctx, c.model, estimated, c.routeID)
	if err != nil {
		return streamAttemptResult{}, err
	}
	maxCompletionTokens := minPositive(reservation.MaxCompletionTokens, outputTokenLimit(ctx))
	ctx = budget.WithOperation(ctx, reservation.OperationID)
	result, err := c.streamChatWithUsageLimit(ctx, messages, definitions, events, true, maxCompletionTokens)
	if !result.emitted && isStreamUsageUnsupported(err) {
		log.Printf("chat_stream_capability_fallback capability=stream_usage model=%s reason=%q", c.model, err.Error())
		result, err = c.streamChatWithUsageLimit(ctx, messages, definitions, events, false, maxCompletionTokens)
	}
	if result.terminal {
		if !result.usage.Valid() {
			result.usage = estimateUsage(messagesToText(messages), result.output)
		}
		if settleErr := settleBudgetedModelCall(ctx, reservation, result.usage); settleErr != nil {
			return result, errors.Join(err, settleErr)
		}
	}
	return result, err
}

func (c *Client) streamMessagesWithUsageOption(ctx context.Context, messages []Message, events chan<- StreamEvent, includeUsage bool) (bool, string, Usage, error) {
	result, err := c.streamChatWithUsageLimit(ctx, messages, nil, events, includeUsage, 0)
	return result.answerEmitted, result.output, result.usage, err
}

func (c *Client) streamChatWithUsageLimit(ctx context.Context, messages []Message, definitions []map[string]any, events chan<- StreamEvent, includeUsage bool, maxCompletionTokens int) (streamAttemptResult, error) {
	const operation = "chat.stream"
	result, err := executeWithRetry(ctx, c.retryPolicy, operation, func() (streamAttemptResult, error) {
		attemptResult, attemptErr := c.streamChatAttempt(ctx, messages, definitions, events, includeUsage, maxCompletionTokens)
		if attemptErr != nil && attemptResult.emitted {
			return attemptResult, withoutRetry(attemptErr, operation)
		}
		return attemptResult, attemptErr
	})
	return result, err
}

type streamAttemptResult struct {
	choice        provider.ChatChoice
	emitted       bool
	answerEmitted bool
	output        string
	usage         Usage
	terminal      bool
	finishReason  string
}

func (c *Client) streamChatAttempt(ctx context.Context, messages []Message, definitions []map[string]any, events chan<- StreamEvent, includeUsage bool, maxCompletionTokens int) (result streamAttemptResult, attemptErr error) {
	const operation = "chat.stream"
	body := map[string]any{
		"model":    c.model,
		"messages": messages,
		"stream":   true,
	}
	profile := c.generationPolicy.AnswerStream
	if len(definitions) > 0 {
		body["tools"], body["tool_choice"] = definitions, "auto"
		// Changing transport must not change the frozen Tool-round sampling.
		profile = c.generationPolicy.Completion
	}
	if err := c.applySampling(body, profile, operation); err != nil {
		return streamAttemptResult{}, err
	}
	if maxCompletionTokens > 0 {
		body["max_tokens"] = maxCompletionTokens
	}
	if includeUsage {
		body["stream_options"] = map[string]any{"include_usage": true}
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return streamAttemptResult{}, err
	}

	modelCallID := budget.OperationFromContext(ctx)
	ref, err := c.recordModelRequest(ctx, modelCallID, operation, c.model, payload)
	if err != nil {
		return streamAttemptResult{}, withoutRetry(err, operation)
	}
	ctx = requestcontrol.WithAttemptTiming(ctx, &requestcontrol.AttemptTiming{})
	started := time.Now()
	var firstToken time.Time
	defer func() {
		c.finishModelAttempt(ctx, ref, started, firstToken, result.usage, result.finishReason, attemptErr)
	}()
	resp, err := c.doRequest(ctx, payload)
	if err != nil {
		return streamAttemptResult{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return streamAttemptResult{}, modelErrorFromHTTPResponse(operation, resp)
	}

	const maxSSEEventBytes = 1024 * 1024
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 4096), maxSSEEventBytes+1)
	var accumulated streamedChoice
	displayStarted := false
	var lastDisplayCheck time.Time
	var lastDisplayText string
	displayCapped := false
	defer func() {
		if !displayStarted {
			return
		}
		display := provider.ReasoningDisplay{ModelCallID: modelCallID, Attempt: ref.Attempt, Format: c.reasoningDisplayFormat, Status: "interrupted"}
		if attemptErr == nil {
			display.Status = "complete"
			display.Text, display.Truncated = reasoningDisplayText(accumulated.reasoning.String(), c.apiKey)
		}
		sendReasoningDisplay(ctx, events, display)
	}()
	answerRetracted := false
	var answerDisplay string
	var answerDisplayTruncated bool
	defer func() {
		if answerRetracted || accumulated.content.Len() == 0 || ctx.Err() != nil {
			return
		}
		text, truncated := sanitizedDisplayPrefix(accumulated.content.String(), c.apiKey, domain.MaxPartialOutputBytes)
		if attemptErr == nil {
			text, truncated = sanitizedDisplayText(accumulated.content.String(), c.apiKey, domain.MaxPartialOutputBytes)
		}
		select {
		case <-ctx.Done():
		case events <- StreamEvent{Type: "output_display", ModelCallID: modelCallID, Attempt: ref.Attempt, DisplayText: &text, DisplayTruncated: truncated}:
		}
	}()
	var data strings.Builder
	finishReasonSeen := false
	refused := false
	processEvent := func() (bool, error) {
		if data.Len() == 0 {
			return false, nil
		}
		eventData := strings.TrimSuffix(data.String(), "\n")
		data.Reset()
		if eventData == "[DONE]" {
			if err := ctx.Err(); err != nil {
				return true, err
			}
			result.terminal = true
			result.output = accumulated.content.String()
			if !result.usage.Valid() {
				if len(definitions) > 0 {
					var generated strings.Builder
					generated.WriteString(result.output)
					generated.WriteString(accumulated.reasoning.String())
					for _, call := range accumulated.calls {
						generated.WriteString(call.name.String())
						generated.WriteString(call.arguments.String())
					}
					result.usage = estimateUsage(string(payload), generated.String())
				} else {
					result.usage = estimateUsage(messagesToText(messages), result.output+accumulated.reasoning.String())
				}
			}
			if !finishReasonSeen {
				result.finishReason = "missing"
				if displayStarted {
					return true, invalidResponseError(operation, "reasoning stream has no finish reason", nil)
				}
			}
			if err := generationOutcomeError(operation, result.finishReason, len(accumulated.calls) > 0, refused); err != nil {
				return true, err
			}
			var err error
			result.choice, err = accumulated.finish()
			if err != nil {
				return true, err
			}
			if len(result.choice.ToolCalls) == 0 && strings.TrimSpace(result.output) == "" {
				return true, invalidResponseError(operation, "model returned an empty final answer", nil)
			}
			return true, nil
		}
		var event chatCompletionChunk
		if err := json.Unmarshal([]byte(eventData), &event); err != nil {
			return false, invalidResponseError(operation, "failed to decode stream event", err)
		}
		if event.Usage != nil {
			result.usage = *event.Usage
		}
		if len(event.Choices) > 1 {
			return false, invalidResponseError(operation, "model stream returned multiple choices", nil)
		}
		for _, choice := range event.Choices {
			if choice.Index != 0 {
				return false, invalidResponseError(operation, "model stream returned an unexpected choice index", nil)
			}
			if finishReasonSeen && (choice.Delta.Content != "" || choice.Delta.Refusal != "" || choice.Delta.ReasoningContent != nil || len(choice.Delta.ToolCalls) > 0) {
				return false, invalidResponseError(operation, "model stream sent content after finish reason", nil)
			}
			if choice.FinishReason != nil {
				if finishReasonSeen {
					return false, invalidResponseError(operation, "model stream sent multiple finish reasons", nil)
				}
				result.finishReason = finishReasonLabel(choice.FinishReason)
				finishReasonSeen = true
			}
			refused = refused || choice.Delta.Refusal != ""
			if len(definitions) == 0 && len(choice.Delta.ToolCalls) > 0 {
				return false, invalidResponseError(operation, "model stream returned unrequested Tool calls", nil)
			}
			// Protocol and usage accounting never depend on display permission.
			if err := accumulated.add(choice.Delta.Content, choice.Delta.ReasoningContent, choice.Delta.ToolCalls); err != nil {
				return false, err
			}
			if !displayStarted && c.reasoningDisplayFormat == provider.ReasoningFormatDeepSeek && modelCallID != "" && choice.Delta.ReasoningContent != nil && strings.TrimSpace(*choice.Delta.ReasoningContent) != "" {
				if !sendReasoningDisplay(ctx, events, provider.ReasoningDisplay{ModelCallID: modelCallID, Attempt: ref.Attempt, Format: c.reasoningDisplayFormat, Status: "receiving"}) {
					return false, ctx.Err()
				}
				displayStarted = true
				// A visible receiving state also forbids transparent retry/replay.
				result.emitted = true
			}
			// Bound both filtering work and live replacements to ten/second. There
			// is no timer goroutine; terminal success always flushes the full copy.
			if displayStarted && !displayCapped && choice.Delta.ReasoningContent != nil && time.Since(lastDisplayCheck) >= 100*time.Millisecond {
				lastDisplayCheck = time.Now()
				text, truncated := reasoningDisplayPrefix(accumulated.reasoning.String(), c.apiKey)
				if text != "" && text != lastDisplayText {
					if !sendReasoningDisplay(ctx, events, provider.ReasoningDisplay{ModelCallID: modelCallID, Attempt: ref.Attempt, Format: c.reasoningDisplayFormat, Status: "receiving", Text: text, Truncated: truncated}) {
						return false, ctx.Err()
					}
					lastDisplayText, displayCapped = text, truncated
				}
			}
			if firstToken.IsZero() && (choice.Delta.Content != "" || len(choice.Delta.ToolCalls) > 0) {
				firstToken = time.Now()
			}
			if len(accumulated.calls) > 0 {
				if result.answerEmitted && !answerRetracted {
					select {
					case <-ctx.Done():
						return false, ctx.Err()
					case events <- StreamEvent{Type: "delta", Reset: true, ModelCallID: modelCallID, Attempt: ref.Attempt}:
						answerRetracted = true
					}
				}
				continue
			}
			if choice.Delta.Content == "" {
				continue
			}
			answerDisplay, answerDisplayTruncated = sanitizedDisplayPrefix(accumulated.content.String(), c.apiKey, domain.MaxPartialOutputBytes)
			display := answerDisplay
			select {
			case <-ctx.Done():
				return false, ctx.Err()
			case events <- StreamEvent{Type: "delta", Delta: choice.Delta.Content, ModelCallID: modelCallID, Attempt: ref.Attempt, DisplayText: &display, DisplayTruncated: answerDisplayTruncated}:
				result.emitted = true
				result.answerEmitted = true
			}
		}
		return false, nil
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			done, err := processEvent()
			if done || err != nil {
				result.output = accumulated.content.String()
				return result, err
			}
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		field := strings.TrimPrefix(line, "data:")
		if strings.HasPrefix(field, " ") {
			field = field[1:]
		}
		if data.Len()+len(field)+1 > maxSSEEventBytes {
			result.output = accumulated.content.String()
			return result, invalidResponseError(operation, "model stream event exceeds size limit", nil)
		}
		data.WriteString(field)
		data.WriteByte('\n')
	}
	result.output = accumulated.content.String()
	if err := scanner.Err(); err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return result, err
		}
		return result, invalidResponseError(operation, "model stream read failed", err)
	}
	return result, invalidResponseError(operation, "model stream ended without [DONE]", nil)
}
