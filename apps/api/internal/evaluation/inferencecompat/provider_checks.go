package inferencecompat

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"strings"
	"sync/atomic"
	"time"

	"agentflow-platform/apps/api/internal/inference/openai"
	"agentflow-platform/apps/api/internal/inference/provider"
	"agentflow-platform/apps/api/internal/inference/requestcontrol"
	"agentflow-platform/apps/api/internal/inference/routing"
)

func checkRouteContract(client *openai.Client, options Options) error {
	identity := client.RuntimeIdentity()
	descriptor := routing.Descriptor{ID: options.RouteID, Provider: identity.Provider, Model: options.Target.Model, Endpoint: identity.BaseURL,
		Capabilities: options.Target.Capabilities, ContextWindowTokens: options.Target.ContextWindow, MaxOutputTokens: options.Target.MaxOutputTokens,
		Priority: 100, CredentialEnvironment: options.CredentialEnv, Pricing: routing.Pricing{Source: "local_compatibility_evidence"}}
	catalog, err := routing.NewCatalog(routing.Binding{Descriptor: descriptor, Client: client})
	if err != nil {
		return err
	}
	_, err = catalog.SelectRoute(options.RouteID, routing.Requirements{Purpose: "compatibility_evidence", Streaming: true, ToolCalling: options.Target.Capabilities.ToolCalling,
		StructuredOutput: options.Target.Capabilities.StructuredOutput, EstimatedInputTokens: 16, MaxOutputTokens: min(64, options.Target.MaxOutputTokens)})
	if err != nil {
		return err
	}
	if !options.Target.Capabilities.ToolCalling {
		_, err = catalog.SelectRoute(options.RouteID, routing.Requirements{Purpose: "unsupported_capability_check", ToolCalling: true, EstimatedInputTokens: 16, MaxOutputTokens: 16})
		if !errors.Is(err, routing.ErrNoCompatibleRoute) {
			return errors.New("Tool Calling was not rejected for a route that declares it unsupported")
		}
	}
	if !options.Target.Capabilities.StructuredOutput {
		_, err = catalog.SelectRoute(options.RouteID, routing.Requirements{Purpose: "unsupported_capability_check", StructuredOutput: true, EstimatedInputTokens: 16, MaxOutputTokens: 16})
		if !errors.Is(err, routing.ErrNoCompatibleRoute) {
			return errors.New("structured output was not rejected for a route that declares it unsupported")
		}
	}
	return nil
}

func checkCompletionUsage(ctx context.Context, client *openai.Client) error {
	result, err := client.CompleteTextDetailed(ctx, "Reply briefly.", "Reply with exactly: compatibility ok")
	if err != nil {
		return err
	}
	if strings.TrimSpace(result.Text) == "" || !result.Usage.Valid() {
		return errors.New("completion or usage is empty")
	}
	if result.Usage.Estimated {
		return errors.New("backend omitted exact usage; adapter used an estimate")
	}
	return nil
}

func checkStreaming(ctx context.Context, client *http.Client, options Options, apiKey string) error {
	response, err := chatRequest(ctx, client, options.Target.BaseURL, apiKey, map[string]any{
		"model": options.Target.Model, "messages": []map[string]string{{"role": "user", "content": "Count from one to three."}},
		"stream": true, "stream_options": map[string]bool{"include_usage": true}, "max_tokens": min(32, options.Target.MaxOutputTokens),
	})
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode/100 != 2 {
		return responseError(response)
	}
	deltas, usage, done, err := readStream(response.Body, nil)
	if err != nil {
		return err
	}
	if deltas == 0 || !done || !usage.Valid() {
		return fmt.Errorf("incomplete stream evidence: deltas=%d done=%t usage=%d", deltas, done, usage.TotalTokens)
	}
	return nil
}

func checkContextLimit(ctx context.Context, client *http.Client, options Options, apiKey string) error {
	response, err := chatRequest(ctx, client, options.Target.BaseURL, apiKey, map[string]any{
		"model": options.Target.Model, "messages": []map[string]string{{"role": "user", "content": strings.Repeat("boundary ", options.Target.ContextWindow+256)}},
		"stream": false, "max_tokens": 16,
	})
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode/100 == 2 {
		return errors.New("backend accepted input beyond the declared context window")
	}
	var body struct {
		Error struct {
			Code    any    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&body); err != nil {
		return fmt.Errorf("backend context error is not JSON: %w", err)
	}
	if body.Error.Code != "context_length_exceeded" && !strings.Contains(strings.ToLower(body.Error.Message), "context") {
		return fmt.Errorf("expected context length error, got status=%d code=%v", response.StatusCode, body.Error.Code)
	}
	return nil
}

func checkTimeout(ctx context.Context, client *http.Client, options Options, apiKey string) error {
	written := make(chan struct{}, 1)
	trace := &httptrace.ClientTrace{WroteRequest: func(httptrace.WroteRequestInfo) { written <- struct{}{} }}
	timeoutCtx, cancel := context.WithTimeout(httptrace.WithClientTrace(ctx, trace), 50*time.Millisecond)
	defer cancel()
	response, err := chatRequest(timeoutCtx, client, options.Target.BaseURL, apiKey, basicPayload(options, 128))
	if err == nil {
		defer response.Body.Close()
		if response.StatusCode/100 != 2 {
			return responseError(response)
		}
		_, _, done, readErr := readStream(response.Body, nil)
		if done {
			return errors.New("stream completed before the timeout")
		}
		err = readErr
	}
	if !errors.Is(timeoutCtx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("expected request timeout while waiting for the stream, got %v", err)
	}
	select {
	case <-written:
	default:
		return errors.New("timeout elapsed before the request was written")
	}
	return probe(ctx, client, options, apiKey)
}

type blockingLimiter struct{ calls atomic.Int32 }

func (l *blockingLimiter) AcquireRequest(ctx context.Context, _ string, _ int) (func(), error) {
	if l.calls.Add(1) == 1 {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return func() {}, nil
}

var _ requestcontrol.Limiter = (*blockingLimiter)(nil)

func checkPermitCancellation(ctx context.Context, client *openai.Client) error {
	limiter := &blockingLimiter{}
	client.SetRequestLimiter(limiter)
	defer client.SetRequestLimiter(nil)
	cancelCtx, cancel := context.WithCancel(ctx)
	timer := time.AfterFunc(10*time.Millisecond, cancel)
	defer timer.Stop()
	defer cancel()
	_, err := client.CompleteTextDetailed(cancelCtx, "Reply briefly.", "permit cancellation")
	modelErr, ok := openai.AsModelError(err)
	if !ok || modelErr.Kind != openai.ErrorCanceled {
		return fmt.Errorf("expected bounded permit cancellation, got %v", err)
	}
	_, err = client.CompleteTextDetailed(ctx, "Reply briefly.", "Reply with exactly: probe ok")
	return err
}

func checkBeforeFirstTokenCancellation(ctx context.Context, client *http.Client, options Options, apiKey string) error {
	cancelCtx, cancel := context.WithCancel(ctx)
	written := make(chan struct{}, 1)
	trace := &httptrace.ClientTrace{WroteRequest: func(httptrace.WroteRequestInfo) { written <- struct{}{} }}
	cancelCtx = httptrace.WithClientTrace(cancelCtx, trace)
	result := make(chan error, 1)
	go func() {
		response, err := chatRequest(cancelCtx, client, options.Target.BaseURL, apiKey, basicPayload(options, options.Target.MaxOutputTokens))
		if response != nil {
			response.Body.Close()
		}
		result <- err
	}()
	select {
	case <-written:
		cancel()
	case <-time.After(options.RequestTimeout):
		cancel()
		return errors.New("request was not written before cancellation deadline")
	}
	select {
	case err := <-result:
		if err == nil || (!errors.Is(err, context.Canceled) && !strings.Contains(strings.ToLower(err.Error()), "canceled")) {
			return fmt.Errorf("expected pre-token cancellation, got %v", err)
		}
	case <-time.After(options.RequestTimeout):
		return errors.New("pre-token cancellation did not finish within the request timeout")
	}
	return probe(ctx, client, options, apiKey)
}

func checkMidGenerationCancellation(ctx context.Context, client *http.Client, options Options, apiKey string) error {
	cancelCtx, cancel := context.WithCancel(ctx)
	response, err := chatRequest(cancelCtx, client, options.Target.BaseURL, apiKey, basicPayload(options, options.Target.MaxOutputTokens))
	if err != nil {
		cancel()
		return err
	}
	if response.StatusCode/100 != 2 {
		cancel()
		defer response.Body.Close()
		return responseError(response)
	}
	seen := make(chan struct{}, 1)
	type outcome struct {
		done bool
		err  error
	}
	result := make(chan outcome, 1)
	go func() {
		_, _, done, readErr := readStream(response.Body, func() { seen <- struct{}{}; cancel() })
		response.Body.Close()
		result <- outcome{done: done, err: readErr}
	}()
	select {
	case <-seen:
	case <-time.After(options.RequestTimeout):
		cancel()
		return errors.New("stream produced no token before cancellation deadline")
	}
	select {
	case observed := <-result:
		if observed.done || observed.err == nil {
			return fmt.Errorf("stream completed instead of stopping mid-generation: done=%t err=%v", observed.done, observed.err)
		}
	case <-time.After(options.RequestTimeout):
		return errors.New("mid-generation cancellation did not finish within the request timeout")
	}
	return probe(ctx, client, options, apiKey)
}

func checkToolCalling(ctx context.Context, client *http.Client, options Options, apiKey string) error {
	response, err := chatRequest(ctx, client, options.Target.BaseURL, apiKey, map[string]any{
		"model": options.Target.Model, "messages": []map[string]string{{"role": "user", "content": "Call get_current_time."}},
		"tools":       []map[string]any{{"type": "function", "function": map[string]any{"name": "get_current_time", "description": "Get time", "parameters": map[string]any{"type": "object", "properties": map[string]any{}}}}},
		"tool_choice": map[string]any{"type": "function", "function": map[string]string{"name": "get_current_time"}}, "stream": false,
	})
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode/100 != 2 {
		return responseError(response)
	}
	var payload struct {
		Choices []struct {
			Message provider.Message `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return err
	}
	if len(payload.Choices) == 0 || len(payload.Choices[0].Message.ToolCalls) == 0 || payload.Choices[0].Message.ToolCalls[0].Function.Name != "get_current_time" {
		return errors.New("route declares Tool Calling but backend returned no valid tool call")
	}
	return nil
}

func checkStructuredOutput(ctx context.Context, client *http.Client, options Options, apiKey string) error {
	response, err := chatRequest(ctx, client, options.Target.BaseURL, apiKey, map[string]any{
		"model": options.Target.Model, "messages": []map[string]string{{"role": "user", "content": "Finish."}},
		"response_format": map[string]any{"type": "json_schema", "json_schema": map[string]any{
			"name": "decision", "strict": true, "schema": map[string]any{"type": "object", "properties": map[string]any{
				"done": map[string]any{"type": "boolean"}}, "required": []string{"done"}, "additionalProperties": false},
		}}, "max_tokens": 64,
	})
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode/100 != 2 {
		return responseError(response)
	}
	var payload struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return err
	}
	if len(payload.Choices) == 0 {
		return errors.New("route declares structured output but backend returned no choices")
	}
	var decision struct {
		Done *bool `json:"done"`
	}
	if json.Unmarshal([]byte(payload.Choices[0].Message.Content), &decision) != nil || decision.Done == nil {
		return errors.New("route declares structured output but backend returned invalid schema content")
	}
	return nil
}

func probe(ctx context.Context, client *http.Client, options Options, apiKey string) error {
	probeCtx, cancel := context.WithTimeout(ctx, options.RequestTimeout)
	defer cancel()
	response, err := chatRequest(probeCtx, client, options.Target.BaseURL, apiKey, map[string]any{
		"model": options.Target.Model, "messages": []map[string]string{{"role": "user", "content": "Reply with OK."}}, "stream": false, "max_tokens": 8,
	})
	if err != nil {
		return fmt.Errorf("capacity recovery probe failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode/100 != 2 {
		return fmt.Errorf("capacity recovery probe: %w", responseError(response))
	}
	var completion struct {
		Choices []struct {
			Message provider.Message `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(response.Body).Decode(&completion); err != nil || len(completion.Choices) == 0 || strings.TrimSpace(completion.Choices[0].Message.Content) == "" {
		return errors.New("capacity recovery probe returned no completion")
	}
	return nil
}

func basicPayload(options Options, maxTokens int) map[string]any {
	return map[string]any{"model": options.Target.Model, "messages": []map[string]string{{"role": "user", "content": "Write a long numbered list."}},
		"stream": true, "stream_options": map[string]bool{"include_usage": true}, "max_tokens": maxTokens}
}

func chatRequest(ctx context.Context, client *http.Client, baseURL, apiKey string, payload any) (*http.Response, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(baseURL, "/")+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Close = true
	if apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+apiKey)
	}
	return client.Do(request)
}

func readStream(body io.Reader, onFirstDelta func()) (int, provider.Usage, bool, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	deltas := 0
	var usage provider.Usage
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			return deltas, usage, true, nil
		}
		var chunk struct {
			Choices []struct {
				Delta provider.Message `json:"delta"`
			} `json:"choices"`
			Usage *provider.Usage `json:"usage"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return deltas, usage, false, err
		}
		if chunk.Usage != nil {
			usage = *chunk.Usage
		}
		for _, choice := range chunk.Choices {
			if choice.Delta.Content != "" {
				deltas++
				if deltas == 1 && onFirstDelta != nil {
					onFirstDelta()
				}
			}
		}
	}
	return deltas, usage, false, scanner.Err()
}

func responseError(response *http.Response) error {
	return fmt.Errorf("status=%d", response.StatusCode)
}
