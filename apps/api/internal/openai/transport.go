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

	"agentflow-platform/apps/api/internal/budget"
	"agentflow-platform/apps/api/internal/modelrequest"
)

func (c *Client) complete(ctx context.Context, body map[string]any) (chatCompletionResponse, error) {
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
		if err := c.recordModelRequest(ctx, reservation.OperationID, operation, c.model, payload); err != nil {
			return chatCompletionResponse{}, withoutRetry(err, operation)
		}
		resp, err := c.doRequest(ctx, payload)
		if err != nil {
			return chatCompletionResponse{}, err
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return chatCompletionResponse{}, modelErrorFromHTTPResponse(operation, resp)
		}

		var decoded chatCompletionResponse
		if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
			return chatCompletionResponse{}, invalidResponseError(operation, "failed to decode model response", err)
		}
		if len(decoded.Choices) == 0 {
			return chatCompletionResponse{}, invalidResponseError(operation, "model returned no choices", nil)
		}
		return decoded, nil
	})
	if err != nil {
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

func (c *Client) streamMessages(ctx context.Context, messages []Message, events chan<- StreamEvent) (bool, string, Usage, error) {
	reservation, err := beginBudgetedModelCall(ctx, c.model, estimateTokens(messagesToText(messages)))
	if err != nil {
		return false, "", Usage{}, err
	}
	maxCompletionTokens := minPositive(reservation.MaxCompletionTokens, outputTokenLimit(ctx))
	ctx = budget.WithOperation(ctx, reservation.OperationID)
	emitted, output, usage, err := c.streamMessagesWithUsageLimit(ctx, messages, events, true, maxCompletionTokens)
	if err == nil {
		if !usage.Valid() {
			usage = estimateUsage(messagesToText(messages), output)
		}
		if settleErr := settleBudgetedModelCall(ctx, reservation, usage); settleErr != nil {
			return emitted, output, usage, settleErr
		}
		return emitted, output, usage, nil
	}
	if !isStreamUsageUnsupported(err) {
		return emitted, output, usage, err
	}
	log.Printf("chat_stream_capability_fallback capability=stream_usage model=%s reason=%q", c.model, err.Error())
	emitted, output, usage, err = c.streamMessagesWithUsageLimit(ctx, messages, events, false, maxCompletionTokens)
	if err != nil {
		return emitted, output, usage, err
	}
	if !usage.Valid() {
		usage = estimateUsage(messagesToText(messages), output)
	}
	if err := settleBudgetedModelCall(ctx, reservation, usage); err != nil {
		return emitted, output, usage, err
	}
	return emitted, output, usage, nil
}

func (c *Client) streamMessagesWithUsageOption(ctx context.Context, messages []Message, events chan<- StreamEvent, includeUsage bool) (bool, string, Usage, error) {
	return c.streamMessagesWithUsageLimit(ctx, messages, events, includeUsage, 0)
}

func (c *Client) streamMessagesWithUsageLimit(ctx context.Context, messages []Message, events chan<- StreamEvent, includeUsage bool, maxCompletionTokens int) (bool, string, Usage, error) {
	const operation = "chat.stream"
	result, err := executeWithRetry(ctx, c.retryPolicy, operation, func() (streamAttemptResult, error) {
		attemptResult, attemptErr := c.streamMessagesAttempt(ctx, messages, events, includeUsage, maxCompletionTokens)
		if attemptErr != nil && attemptResult.emitted {
			return attemptResult, withoutRetry(attemptErr, operation)
		}
		return attemptResult, attemptErr
	})
	return result.emitted, result.output, result.usage, err
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
	emitted bool
	output  string
	usage   Usage
}

func (c *Client) streamMessagesAttempt(ctx context.Context, messages []Message, events chan<- StreamEvent, includeUsage bool, maxCompletionTokens int) (streamAttemptResult, error) {
	const operation = "chat.stream"
	body := map[string]any{
		"model":       c.model,
		"messages":    messages,
		"stream":      true,
		"temperature": 0.4,
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
	if err := c.recordModelRequest(ctx, modelCallID, operation, c.model, payload); err != nil {
		return streamAttemptResult{}, withoutRetry(err, operation)
	}
	resp, err := c.doRequest(ctx, payload)
	if err != nil {
		return streamAttemptResult{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return streamAttemptResult{}, modelErrorFromHTTPResponse(operation, resp)
	}

	scanner := bufio.NewScanner(resp.Body)
	buf := make([]byte, 0, 1024*1024)
	scanner.Buffer(buf, 1024*1024)

	emitted := false
	var output strings.Builder
	var usage Usage
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			return streamAttemptResult{emitted: emitted, output: output.String(), usage: usage}, nil
		}

		var event chatCompletionChunk
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			return streamAttemptResult{emitted: emitted, output: output.String(), usage: usage}, invalidResponseError(operation, "failed to decode stream event", err)
		}
		if event.Usage != nil {
			usage = *event.Usage
		}
		for _, choice := range event.Choices {
			if choice.Delta.Content == "" {
				continue
			}
			emitted = true
			output.WriteString(choice.Delta.Content)
			select {
			case <-ctx.Done():
				return streamAttemptResult{emitted: emitted, output: output.String(), usage: usage}, ctx.Err()
			case events <- StreamEvent{Type: "delta", Delta: choice.Delta.Content}:
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return streamAttemptResult{emitted: emitted, output: output.String(), usage: usage}, invalidResponseError(operation, "model stream read failed", err)
	}
	return streamAttemptResult{emitted: emitted, output: output.String(), usage: usage}, invalidResponseError(operation, "model stream ended without [DONE]", nil)
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
	var limitErr *modelrequest.TokenBucketCapacityError
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
