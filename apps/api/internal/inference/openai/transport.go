package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"agentflow-platform/apps/api/internal/budget"
	"agentflow-platform/apps/api/internal/inference/requestcontrol"
)

func (c *Client) complete(ctx context.Context, body map[string]any) (chatCompletionResponse, error) {
	if err := c.applySampling(body, c.generationPolicy.Completion, "chat.completion"); err != nil {
		return chatCompletionResponse{}, err
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return chatCompletionResponse{}, err
	}
	reservation, err := beginBudgetedModelCall(ctx, c.model, estimatedRequestTokens(payload))
	if err != nil {
		return chatCompletionResponse{}, err
	}
	if maxTokens := minPositive(reservation.MaxCompletionTokens, outputTokenLimit(ctx)); maxTokens > 0 {
		body["max_tokens"] = maxTokens
		payload, err = json.Marshal(body)
		if err != nil {
			return chatCompletionResponse{}, err
		}
	}
	ctx = budget.WithOperation(ctx, reservation.OperationID)
	const operation = "chat.completion"
	response, err := executeWithRetry(ctx, c.retryPolicy, operation, func() (chatCompletionResponse, error) {
		return c.completeAttempt(ctx, reservation.OperationID, operation, payload)
	})
	if err != nil {
		if len(response.Choices) > 0 && response.Usage.Valid() {
			if settleErr := settleBudgetedModelCall(ctx, reservation, response.Usage); settleErr != nil {
				return response, errors.Join(err, settleErr)
			}
		}
		return response, err
	}
	if !response.Usage.Valid() {
		response.Usage = estimateUsage(string(payload), response.Choices[0].Message.Content)
	}
	if err := settleBudgetedModelCall(ctx, reservation, response.Usage); err != nil {
		return response, err
	}
	return response, nil
}

func (c *Client) completeAttempt(ctx context.Context, modelCallID, operation string, payload []byte) (decoded chatCompletionResponse, attemptErr error) {
	ref, err := c.recordModelRequest(ctx, modelCallID, operation, c.model, payload)
	if err != nil {
		return decoded, withoutRetry(err, operation)
	}
	started := time.Now()
	defer func() {
		reason := ""
		if len(decoded.Choices) > 0 {
			reason = finishReasonLabel(decoded.Choices[0].FinishReason)
		}
		c.finishModelAttempt(ctx, ref, started, time.Time{}, decoded.Usage, reason, attemptErr)
	}()
	resp, err := c.doRequest(ctx, payload)
	if err != nil {
		return decoded, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return decoded, modelErrorFromHTTPResponse(operation, resp)
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return decoded, invalidResponseError(operation, "failed to decode model response", err)
	}
	if len(decoded.Choices) == 0 {
		return decoded, invalidResponseError(operation, "model returned no choices", nil)
	}
	if !decoded.Usage.Valid() {
		decoded.Usage = estimateUsage(string(payload), decoded.Choices[0].Message.Content)
	}
	choice := decoded.Choices[0]
	if err := generationOutcomeError(operation, finishReasonLabel(choice.FinishReason), len(choice.Message.ToolCalls) > 0, choice.Message.Refusal != ""); err != nil {
		return decoded, err
	}
	return decoded, nil
}

func finishReasonLabel(reason *string) string {
	if reason == nil {
		return "missing"
	}
	switch *reason {
	case "stop", "tool_calls", "length", "content_filter":
		return *reason
	default:
		return "unknown"
	}
}

func generationOutcomeError(operation, label string, hasToolCalls, refused bool) error {
	if refused || label == "content_filter" {
		return &ModelError{Kind: ErrorContentPolicy, Operation: operation, Message: "generation blocked by provider content policy"}
	}
	switch label {
	case "missing":
		// Some OpenAI-compatible providers omit finish_reason. Record this as
		// unconfirmed evidence; [DONE] alone does not prove a normal stop.
		if hasToolCalls {
			return &ModelError{Kind: ErrorInvalidResponse, Operation: operation, Message: "tool calls require a confirmed generation finish reason"}
		}
		return nil
	case "stop":
		if !hasToolCalls {
			return nil
		}
	case "tool_calls":
		if hasToolCalls {
			return nil
		}
	case "length":
		return &ModelError{Kind: ErrorIncompleteOutput, Operation: operation, Message: "generation reached the output limit"}
	}
	return &ModelError{Kind: ErrorInvalidResponse, Operation: operation, Message: "unrecognized or inconsistent generation finish reason"}
}

func (c *Client) streamMessages(ctx context.Context, messages []Message, events chan<- StreamEvent) (bool, string, Usage, error) {
	if err := c.validateSampling("chat.stream"); err != nil {
		return false, "", Usage{}, err
	}
	reservation, err := beginBudgetedModelCall(ctx, c.model, estimateTokens(messagesToText(messages)))
	if err != nil {
		return false, "", Usage{}, err
	}
	maxCompletionTokens := minPositive(reservation.MaxCompletionTokens, outputTokenLimit(ctx))
	ctx = budget.WithOperation(ctx, reservation.OperationID)
	result, err := c.streamMessagesWithUsageLimit(ctx, messages, events, true, maxCompletionTokens)
	if isStreamUsageUnsupported(err) {
		log.Printf("chat_stream_capability_fallback capability=stream_usage model=%s reason=%q", c.model, err.Error())
		result, err = c.streamMessagesWithUsageLimit(ctx, messages, events, false, maxCompletionTokens)
	}
	if result.terminal {
		if !result.usage.Valid() {
			result.usage = estimateUsage(messagesToText(messages), result.output)
		}
		if settleErr := settleBudgetedModelCall(ctx, reservation, result.usage); settleErr != nil {
			return result.emitted, result.output, result.usage, errors.Join(err, settleErr)
		}
	}
	return result.emitted, result.output, result.usage, err
}

func (c *Client) streamMessagesWithUsageOption(ctx context.Context, messages []Message, events chan<- StreamEvent, includeUsage bool) (bool, string, Usage, error) {
	result, err := c.streamMessagesWithUsageLimit(ctx, messages, events, includeUsage, 0)
	return result.emitted, result.output, result.usage, err
}

func (c *Client) streamMessagesWithUsageLimit(ctx context.Context, messages []Message, events chan<- StreamEvent, includeUsage bool, maxCompletionTokens int) (streamAttemptResult, error) {
	const operation = "chat.stream"
	result, err := executeWithRetry(ctx, c.retryPolicy, operation, func() (streamAttemptResult, error) {
		attemptResult, attemptErr := c.streamMessagesAttempt(ctx, messages, events, includeUsage, maxCompletionTokens)
		if attemptErr != nil && attemptResult.emitted {
			return attemptResult, withoutRetry(attemptErr, operation)
		}
		return attemptResult, attemptErr
	})
	return result, err
}

func beginBudgetedModelCall(ctx context.Context, model string, estimatedPromptTokens int) (budget.ModelReservation, error) {
	controller := budget.FromContext(ctx)
	if controller == nil {
		operationID := budget.OperationFromContext(ctx)
		if operationID == "" {
			operationID = budget.NewOperationID("model")
		}
		return budget.ModelReservation{OperationID: operationID, Model: model, EstimatedPromptTokens: estimatedPromptTokens}, nil
	}
	operationID := budget.OperationFromContext(ctx)
	if operationID == "" {
		operationID = budget.NewOperationID("model")
	}
	return controller.BeginModelCall(ctx, budget.ModelCallEstimate{
		OperationID: operationID, Purpose: budget.PurposeFromContext(ctx),
		Model: model, EstimatedPromptTokens: estimatedPromptTokens,
	})
}

func settleBudgetedModelCall(ctx context.Context, reservation budget.ModelReservation, usage Usage) error {
	controller := budget.FromContext(ctx)
	if controller == nil {
		return nil
	}
	return controller.SettleModelCall(ctx, reservation, budget.ModelUsage{
		PromptTokens: usage.PromptTokens, CompletionTokens: usage.CompletionTokens,
		TotalTokens: usage.TotalTokens, Estimated: usage.Estimated,
	})
}

type outputTokenLimitKey struct{}

func withOutputTokenLimit(ctx context.Context, limit int) context.Context {
	if limit <= 0 {
		return ctx
	}
	return context.WithValue(ctx, outputTokenLimitKey{}, limit)
}

func outputTokenLimit(ctx context.Context) int {
	limit, _ := ctx.Value(outputTokenLimitKey{}).(int)
	return max(0, limit)
}

func minPositive(values ...int) int {
	result := 0
	for _, value := range values {
		if value > 0 && (result == 0 || value < result) {
			result = value
		}
	}
	return result
}

type streamAttemptResult struct {
	emitted      bool
	output       string
	usage        Usage
	terminal     bool
	finishReason string
}

func (c *Client) streamMessagesAttempt(ctx context.Context, messages []Message, events chan<- StreamEvent, includeUsage bool, maxCompletionTokens int) (result streamAttemptResult, attemptErr error) {
	const operation = "chat.stream"
	body := map[string]any{
		"model":    c.model,
		"messages": messages,
		"stream":   true,
	}
	if err := c.applySampling(body, c.generationPolicy.AnswerStream, operation); err != nil {
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
	var output strings.Builder
	var data strings.Builder
	finishReasonSeen := false
	refused := false
	processEvent := func() (bool, error) {
		if data.Len() == 0 {
			return false, nil
		}
		payload := strings.TrimSuffix(data.String(), "\n")
		data.Reset()
		if payload == "[DONE]" {
			if err := ctx.Err(); err != nil {
				return true, err
			}
			result.terminal = true
			result.output = output.String()
			if !result.usage.Valid() {
				result.usage = estimateUsage(messagesToText(messages), result.output)
			}
			if !finishReasonSeen {
				result.finishReason = "missing"
			}
			if err := generationOutcomeError(operation, result.finishReason, false, refused); err != nil {
				return true, err
			}
			return true, nil
		}
		var event chatCompletionChunk
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			return false, invalidResponseError(operation, "failed to decode stream event", err)
		}
		if event.Usage != nil {
			result.usage = *event.Usage
		}
		for _, choice := range event.Choices {
			if finishReasonSeen && (choice.Delta.Content != "" || choice.Delta.Refusal != "") {
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
			if choice.Delta.Content == "" {
				continue
			}
			if firstToken.IsZero() {
				firstToken = time.Now()
			}
			result.emitted = true
			output.WriteString(choice.Delta.Content)
			select {
			case <-ctx.Done():
				return false, ctx.Err()
			case events <- StreamEvent{Type: "delta", Delta: choice.Delta.Content}:
			}
		}
		return false, nil
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			done, err := processEvent()
			if done || err != nil {
				result.output = output.String()
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
			result.output = output.String()
			return result, invalidResponseError(operation, "model stream event exceeds size limit", nil)
		}
		data.WriteString(field)
		data.WriteByte('\n')
	}
	result.output = output.String()
	if err := scanner.Err(); err != nil {
		return result, invalidResponseError(operation, "model stream read failed", err)
	}
	return result, invalidResponseError(operation, "model stream ended without [DONE]", nil)
}

func (c *Client) doRequest(ctx context.Context, payload []byte) (*http.Response, error) {
	return c.doPathRequest(ctx, c.baseURL, "/chat/completions", payload)
}

func (c *Client) doEmbeddingRequest(ctx context.Context, payload []byte) (*http.Response, error) {
	return c.doPathRequest(ctx, c.embeddingBaseURL, "/embeddings", payload)
}

func (c *Client) doRawRequest(ctx context.Context, url string, payload []byte) (*http.Response, error) {
	return c.doPathRequest(ctx, url, "", payload)
}

func (c *Client) doPathRequest(ctx context.Context, baseURL string, path string, payload []byte) (*http.Response, error) {
	release, err := c.acquireRequestPermit(ctx, estimatedRequestTokens(payload))
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+path, bytes.NewReader(payload))
	if err != nil {
		release()
		return nil, err
	}
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("HTTP-Referer", "http://localhost:3000")
	req.Header.Set("X-Title", "AgentFlow Platform")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		release()
		return nil, err
	}
	resp.Body = &releaseReadCloser{ReadCloser: resp.Body, release: release}
	return resp, nil
}

func (c *Client) acquireRequestPermit(ctx context.Context, estimatedTokens int) (func(), error) {
	if c.requestLimiter == nil {
		return func() {}, nil
	}
	release, err := c.requestLimiter.AcquireRequest(ctx, c.apiKey, estimatedTokens)
	var limitErr *requestcontrol.TokenBucketCapacityError
	if errors.As(err, &limitErr) {
		return nil, &ModelError{
			Kind:    ErrorRequestTokenCapacity,
			Message: "estimated request exceeds local TPM bucket capacity",
			Cause:   err,
		}
	}
	return release, err
}

func estimatedRequestTokens(payload []byte) int {
	return max(1, (len(payload)+3)/4)
}

type releaseReadCloser struct {
	io.ReadCloser
	release func()
	once    sync.Once
}

func (r *releaseReadCloser) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	if err != nil {
		r.once.Do(r.release)
	}
	return n, err
}

func (r *releaseReadCloser) Close() error {
	err := r.ReadCloser.Close()
	r.once.Do(r.release)
	return err
}
