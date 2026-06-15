package openai

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net"
	"net/http"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/tools"
	tracepkg "agentflow-platform/apps/api/internal/trace"
)

type Client struct {
	apiKey         string
	baseURL        string
	model          string
	embeddingModel string
	embeddingDims  int
	httpClient     *http.Client
	timeout        time.Duration
}

type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
}

type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type StreamEvent struct {
	Type  string
	Delta string
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
	Estimated        bool
}

type TextCompletion struct {
	Text  string
	Model string
	Usage Usage
}

type Embedding struct {
	Vector     []float64
	Model      string
	Provider   string
	Estimated  bool
	Dimensions int
}

func NewClient(apiKey string, baseURL string, model string) *Client {
	return NewClientWithTimeout(apiKey, baseURL, model, 5*time.Minute)
}

func NewClientWithTimeout(apiKey string, baseURL string, model string, timeout time.Duration) *Client {
	return NewClientWithTimeoutAndEmbeddingModel(apiKey, baseURL, model, "text-embedding-3-small", 1536, timeout)
}

func NewClientWithTimeoutAndEmbeddingModel(apiKey string, baseURL string, model string, embeddingModel string, embeddingDims int, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	embeddingModel = strings.TrimSpace(embeddingModel)
	if embeddingModel == "" {
		embeddingModel = "text-embedding-3-small"
	}
	if embeddingDims <= 0 {
		embeddingDims = 1536
	}
	return &Client{
		apiKey:         strings.TrimSpace(apiKey),
		baseURL:        normalizeBaseURL(baseURL),
		model:          strings.TrimSpace(model),
		embeddingModel: embeddingModel,
		embeddingDims:  embeddingDims,
		httpClient: &http.Client{
			Transport: &http.Transport{
				Proxy:                 http.ProxyFromEnvironment,
				DialContext:           (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
				ForceAttemptHTTP2:     true,
				TLSHandshakeTimeout:   15 * time.Second,
				ResponseHeaderTimeout: timeout,
				ExpectContinueTimeout: 1 * time.Second,
			},
		},
		timeout: timeout,
	}
}

func (c *Client) HasAPIKey() bool {
	return c.apiKey != ""
}

func (c *Client) StreamChat(ctx context.Context, history []domain.Message, latest string) (<-chan string, <-chan error) {
	chunks := make(chan string)
	errs := make(chan error, 1)

	go func() {
		defer close(chunks)
		defer close(errs)

		if c.apiKey == "" {
			log.Printf("chat_fallback mode=local_no_api_key latest_len=%d", len(latest))
			c.streamFallback(ctx, latest, chunks)
			return
		}

		messages := buildMessages(history)
		events := make(chan StreamEvent)
		go func() {
			defer close(events)
			if _, _, _, err := c.streamMessages(ctx, messages, events); err != nil {
				errs <- err
			}
		}()
		for event := range events {
			if event.Type == "delta" {
				chunks <- event.Delta
			}
		}
	}()

	return chunks, errs
}

func (c *Client) StreamChatWithTools(ctx context.Context, history []domain.Message, latest string, registry *tools.Registry) (<-chan StreamEvent, <-chan error) {
	return c.StreamAgentChatWithTools(ctx, "You are AgentFlow's assistant. Use tools when they help.", history, latest, registry)
}

func (c *Client) StreamAgentChatWithTools(ctx context.Context, systemPrompt string, history []domain.Message, latest string, registry *tools.Registry) (<-chan StreamEvent, <-chan error) {
	return c.StreamAgentChatWithToolsTrace(ctx, systemPrompt, history, latest, registry, nil, "", "", nil, nil)
}

func (c *Client) StreamAgentChatWithToolsTrace(ctx context.Context, systemPrompt string, history []domain.Message, latest string, registry *tools.Registry, recorder *tracepkg.Recorder, runID string, stepID string, retrievedMemories []domain.RetrievedMemory, retrievedChunks []domain.RetrievedDocumentChunk) (<-chan StreamEvent, <-chan error) {
	events := make(chan StreamEvent)
	errs := make(chan error, 1)

	go func() {
		defer close(events)
		defer close(errs)

		if c.apiKey == "" {
			log.Printf("chat_fallback mode=local_no_api_key latest_len=%d enabled_tools=%q", len(latest), strings.Join(registry.EnabledNames(), ","))
			output := fallbackEventResponse(latest)
			startPayload := map[string]any{
				"model":       "local_fallback",
				"system":      systemPrompt,
				"input":       latest,
				"input_chars": len(latest),
			}
			if len(retrievedMemories) > 0 {
				startPayload["retrieved_memories"] = retrievedMemoryPayload(retrievedMemories)
			}
			if len(retrievedChunks) > 0 {
				startPayload["retrieved_chunks"] = retrievedChunkPayload(retrievedChunks)
			}
			span := recorder.LLMStart(ctx, runID, stepID, startPayload)
			c.streamText(ctx, output, 45*time.Millisecond, events)
			recorder.LLMEnd(ctx, span, tokenPayload(map[string]any{
				"model":        "local_fallback",
				"output":       output,
				"output_chars": len(output),
			}, estimateUsage(systemPrompt+"\n"+latest, output)))
			return
		}

		if err := c.streamOpenAIWithTools(ctx, systemPrompt, history, registry, events, recorder, runID, stepID, retrievedMemories, retrievedChunks); err != nil {
			recorder.Error(ctx, runID, stepID, map[string]any{
				"source": "llm",
				"model":  c.model,
				"error":  err.Error(),
			})
			errs <- err
		}
	}()

	return events, errs
}

func (c *Client) CompleteText(ctx context.Context, systemPrompt string, prompt string) (string, error) {
	completion, err := c.CompleteTextDetailed(ctx, systemPrompt, prompt)
	if err != nil {
		return "", err
	}
	return completion.Text, nil
}

func (c *Client) CompleteTextDetailed(ctx context.Context, systemPrompt string, prompt string) (TextCompletion, error) {
	prompt = strings.TrimSpace(prompt)
	if c.apiKey == "" {
		text := fallbackCompletion(systemPrompt, prompt)
		return TextCompletion{
			Text:  text,
			Model: "local_fallback",
			Usage: estimateUsage(systemPrompt+"\n"+prompt, text),
		}, nil
	}

	messages := []Message{
		{Role: "system", Content: strings.TrimSpace(systemPrompt)},
		{Role: "user", Content: prompt},
	}
	response, err := c.complete(ctx, map[string]any{
		"model":       c.model,
		"messages":    messages,
		"temperature": 0.2,
	})
	if err != nil {
		return TextCompletion{}, err
	}
	usage := response.Usage
	if !usage.Valid() {
		usage = estimateUsage(strings.TrimSpace(systemPrompt)+"\n"+prompt, strings.TrimSpace(response.Choices[0].Message.Content))
	}
	return TextCompletion{
		Text:  strings.TrimSpace(response.Choices[0].Message.Content),
		Model: c.model,
		Usage: usage,
	}, nil
}

func (c *Client) EmbedText(ctx context.Context, input string) (Embedding, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return Embedding{}, errors.New("embedding input is required")
	}
	if c.apiKey == "" {
		return Embedding{
			Vector:     deterministicEmbedding(input, c.embeddingDims),
			Model:      "local_hash_embedding",
			Provider:   "local",
			Estimated:  true,
			Dimensions: c.embeddingDims,
		}, nil
	}
	payload, err := c.embeddingRequestPayload(input)
	if err != nil {
		return Embedding{}, err
	}
	resp, err := c.doPathRequest(ctx, "/embeddings", payload)
	if err != nil {
		return Embedding{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bytes, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return Embedding{}, fmt.Errorf("openai-compatible embedding request failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(bytes)))
	}

	var decoded embeddingResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return Embedding{}, err
	}
	if len(decoded.Data) == 0 || len(decoded.Data[0].Embedding) == 0 {
		return Embedding{}, errors.New("embedding response returned no vector")
	}
	return Embedding{
		Vector:     decoded.Data[0].Embedding,
		Model:      decoded.Model,
		Provider:   "openai_compatible",
		Dimensions: len(decoded.Data[0].Embedding),
	}, nil
}

func (c *Client) embeddingRequestPayload(input string) ([]byte, error) {
	request := map[string]any{
		"model": c.embeddingModel,
		"input": input,
	}
	if c.embeddingDims > 0 {
		request["dimensions"] = c.embeddingDims
	}
	return json.Marshal(request)
}

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
		return "Review:\n- The result follows the requested fixed collaboration flow.\n- No automatic retry was performed in this first version.\n- Remaining risk: verify domain-specific details when the task depends on external facts."
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

func (c *Client) streamOpenAIWithTools(ctx context.Context, systemPrompt string, history []domain.Message, registry *tools.Registry, events chan<- StreamEvent, recorder *tracepkg.Recorder, runID string, stepID string, retrievedMemories []domain.RetrievedMemory, retrievedChunks []domain.RetrievedDocumentChunk) error {
	enabledTools := registry.EnabledNames()
	messages := buildMessagesWithSystemPrompt(systemPrompt, history, enabledTools, retrievedMemories, retrievedChunks)
	startPayload := map[string]any{
		"model":         c.model,
		"messages":      messages,
		"enabled_tools": enabledTools,
		"input_chars":   messagesTextLength(messages),
	}
	if len(retrievedMemories) > 0 {
		startPayload["retrieved_memories"] = retrievedMemoryPayload(retrievedMemories)
	}
	if len(retrievedChunks) > 0 {
		startPayload["retrieved_chunks"] = retrievedChunkPayload(retrievedChunks)
	}
	llmSpan := recorder.LLMStart(ctx, runID, stepID, startPayload)
	decision, err := c.complete(ctx, map[string]any{
		"model":       c.model,
		"messages":    messages,
		"tools":       registry.Definitions(),
		"tool_choice": "auto",
		"temperature": 0.2,
	})
	if err != nil {
		recorder.Error(ctx, runID, stepID, map[string]any{
			"source": "llm",
			"stage":  "tool_selection",
			"model":  c.model,
			"error":  err.Error(),
		})
		log.Printf(
			"chat_fallback mode=openai_without_tools reason=%q model=%s enabled_tools=%q history_messages=%d",
			err.Error(),
			c.model,
			strings.Join(enabledTools, ","),
			len(messages),
		)
		decision, err = c.complete(ctx, map[string]any{
			"model":       c.model,
			"messages":    messages,
			"temperature": 0.2,
		})
		if err != nil {
			recorder.Error(ctx, runID, stepID, map[string]any{
				"source": "llm",
				"stage":  "text_fallback",
				"model":  c.model,
				"error":  err.Error(),
			})
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

	messages = append(messages, Message{
		Role:      "assistant",
		Content:   choice.Message.Content,
		ToolCalls: normalizeToolCalls(toolCalls),
	})

	results := make([]tools.ExecutionResult, 0, len(toolCalls))
	for _, call := range normalizeToolCalls(toolCalls) {
		log.Printf("tool_call_start id=%s tool=%s arguments=%q", call.ID, call.Function.Name, call.Function.Arguments)
		toolSpan := recorder.ToolStart(ctx, runID, stepID, map[string]any{
			"tool_call_id": call.ID,
			"tool_name":    call.Function.Name,
			"arguments":    json.RawMessage(call.Function.Arguments),
		})

		result := registry.Execute(ctx, call.Function.Name, json.RawMessage(call.Function.Arguments))
		results = append(results, result)
		resultText := marshalResult(result)
		toolPayload := map[string]any{
			"tool_call_id": call.ID,
			"tool_name":    call.Function.Name,
			"arguments":    string(result.Arguments),
			"result":       result.Result,
			"error":        result.Error,
		}
		if result.Error != "" {
			recorder.Error(ctx, runID, stepID, map[string]any{
				"source":       "tool",
				"tool_call_id": call.ID,
				"tool_name":    call.Function.Name,
				"arguments":    string(result.Arguments),
				"error":        result.Error,
			})
		}
		recorder.ToolEnd(ctx, toolSpan, toolPayload)
		logStatus := "tool_end"
		if result.Error != "" {
			logStatus = "tool_error"
		}
		log.Printf(
			"tool_call_end id=%s tool=%s status=%s duration_ms=%d arguments=%q result=%q error=%q",
			call.ID,
			call.Function.Name,
			logStatus,
			result.DurationMS,
			string(result.Arguments),
			resultText,
			result.Error,
		)

		messages = append(messages, Message{
			Role:       "tool",
			ToolCallID: call.ID,
			Content:    resultText,
		})
	}

	emitted, finalOutput, finalUsage, err := c.streamMessages(ctx, messages, events)
	if err != nil {
		recorder.Error(ctx, runID, stepID, map[string]any{
			"source": "llm",
			"stage":  "final_stream",
			"model":  c.model,
			"error":  err.Error(),
		})
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
		"output":       finalOutput,
		"output_chars": len(finalOutput),
	}, finalUsage))
	return nil
}

func (c *Client) complete(ctx context.Context, body map[string]any) (chatCompletionResponse, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return chatCompletionResponse{}, err
	}

	resp, err := c.doRequest(ctx, payload)
	if err != nil {
		return chatCompletionResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bytes, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return chatCompletionResponse{}, fmt.Errorf("openai-compatible request failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(bytes)))
	}

	var decoded chatCompletionResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return chatCompletionResponse{}, err
	}
	if len(decoded.Choices) == 0 {
		return chatCompletionResponse{}, errors.New("model returned no choices")
	}
	return decoded, nil
}

func (c *Client) streamMessages(ctx context.Context, messages []Message, events chan<- StreamEvent) (bool, string, Usage, error) {
	emitted, output, usage, err := c.streamMessagesWithUsageOption(ctx, messages, events, true)
	if err == nil {
		return emitted, output, usage, nil
	}
	log.Printf("chat_stream_usage_fallback reason=%q model=%s", err.Error(), c.model)
	return c.streamMessagesWithUsageOption(ctx, messages, events, false)
}

func (c *Client) streamMessagesWithUsageOption(ctx context.Context, messages []Message, events chan<- StreamEvent, includeUsage bool) (bool, string, Usage, error) {
	body := map[string]any{
		"model":       c.model,
		"messages":    messages,
		"stream":      true,
		"temperature": 0.4,
	}
	if includeUsage {
		body["stream_options"] = map[string]any{"include_usage": true}
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return false, "", Usage{}, err
	}

	resp, err := c.doRequest(ctx, payload)
	if err != nil {
		return false, "", Usage{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bytes, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return false, "", Usage{}, fmt.Errorf("openai-compatible stream failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(bytes)))
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
			return emitted, output.String(), usage, nil
		}

		var event chatCompletionChunk
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			return false, "", Usage{}, err
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
				return false, output.String(), usage, ctx.Err()
			case events <- StreamEvent{Type: "delta", Delta: choice.Delta.Content}:
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return false, output.String(), usage, err
	}
	return false, output.String(), usage, errors.New("openai-compatible stream ended without [DONE]")
}

func (c *Client) doRequest(ctx context.Context, payload []byte) (*http.Response, error) {
	return c.doPathRequest(ctx, "/chat/completions", payload)
}

func (c *Client) doPathRequest(ctx context.Context, path string, payload []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("HTTP-Referer", "http://localhost:3000")
	req.Header.Set("X-Title", "AgentFlow Platform")
	return c.httpClient.Do(req)
}

type chatCompletionChunk struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
	} `json:"choices"`
	Usage *Usage `json:"usage"`
}

type chatCompletionResponse struct {
	Choices []struct {
		Message Message `json:"message"`
	} `json:"choices"`
	Usage Usage `json:"usage"`
}

type embeddingResponse struct {
	Model string `json:"model"`
	Data  []struct {
		Embedding []float64 `json:"embedding"`
	} `json:"data"`
}

func buildMessages(history []domain.Message) []Message {
	return buildMessagesWithToolNames(history, []string{"calculator", "get_current_time", "mock_web_search"})
}

func buildMessagesWithToolNames(history []domain.Message, toolNames []string) []Message {
	return buildMessagesWithSystemPrompt("You are AgentFlow's assistant. Use tools when they help.", history, toolNames, nil, nil)
}

func buildMessagesWithSystemPrompt(systemPrompt string, history []domain.Message, toolNames []string, retrievedMemories []domain.RetrievedMemory, retrievedChunks []domain.RetrievedDocumentChunk) []Message {
	available := "No tools are currently enabled."
	fallbackInstruction := ""
	if len(toolNames) > 0 {
		available = "Available tools include " + strings.Join(toolNames, ", ") + "."
		fallbackInstruction = " If native tool calling is unavailable, output only JSON like {\"action\":\"tool_call\",\"tool\":\"calculator\",\"arguments\":{\"expression\":\"128 * 37\"}}."
	}
	systemPrompt = strings.TrimSpace(systemPrompt)
	if systemPrompt == "" {
		systemPrompt = "You are AgentFlow's assistant."
	}
	if len(retrievedMemories) > 0 {
		systemPrompt = systemPrompt + "\n\n" + formatRetrievedMemories(retrievedMemories)
	}
	if len(retrievedChunks) > 0 {
		systemPrompt = systemPrompt + "\n\n" + formatRetrievedChunks(retrievedChunks)
	}
	messages := []Message{
		{
			Role:    "system",
			Content: systemPrompt + " " + available + fallbackInstruction,
		},
	}
	for _, item := range history {
		if item.Role == "user" || item.Role == "assistant" {
			messages = append(messages, Message{Role: item.Role, Content: item.Content})
		}
	}
	return messages
}

func formatRetrievedMemories(memories []domain.RetrievedMemory) string {
	var builder strings.Builder
	builder.WriteString("Retrieved memories. Use them when relevant, and ignore them when they are not relevant:\n")
	for index, memory := range memories {
		if strings.TrimSpace(memory.Memory.Content) == "" {
			continue
		}
		builder.WriteString(fmt.Sprintf("%d. id=%s kind=%s score=%.4f similarity=%.4f\n", index+1, memory.Memory.ID, memory.Memory.Kind, memory.Score, memory.Similarity))
		builder.WriteString(truncateText(memory.Memory.Content, 1200))
		builder.WriteString("\n")
	}
	return strings.TrimSpace(builder.String())
}

func formatRetrievedChunks(chunks []domain.RetrievedDocumentChunk) string {
	var builder strings.Builder
	builder.WriteString("Retrieved document chunks. Use them when relevant, and ignore them when they are not relevant:\n")
	for index, chunk := range chunks {
		if strings.TrimSpace(chunk.Chunk.Content) == "" {
			continue
		}
		builder.WriteString(fmt.Sprintf("%d. document=%s chunk=%s score=%.4f similarity=%.4f\n", index+1, chunk.Document.Title, chunk.Chunk.ID, chunk.Score, chunk.Similarity))
		builder.WriteString(truncateText(chunk.Chunk.Content, 1600))
		builder.WriteString("\n")
	}
	return strings.TrimSpace(builder.String())
}

func emitText(ctx context.Context, text string, events chan<- StreamEvent) error {
	if strings.TrimSpace(text) == "" {
		text = "I do not have a response yet."
	}
	parts := strings.SplitAfter(text, " ")
	for _, part := range parts {
		if part == "" {
			continue
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case events <- StreamEvent{Type: "delta", Delta: part}:
			time.Sleep(20 * time.Millisecond)
		}
	}
	return nil
}

func normalizeToolCalls(toolCalls []ToolCall) []ToolCall {
	normalized := make([]ToolCall, 0, len(toolCalls))
	for index, call := range toolCalls {
		if call.ID == "" {
			call.ID = fmt.Sprintf("call_%d", index+1)
		}
		if call.Type == "" {
			call.Type = "function"
		}
		if strings.TrimSpace(call.Function.Arguments) == "" {
			call.Function.Arguments = "{}"
		}
		normalized = append(normalized, call)
	}
	return normalized
}

func marshalResult(result tools.ExecutionResult) string {
	bytes, err := json.Marshal(result)
	if err != nil {
		return fmt.Sprintf(`{"tool":%q,"error":%q}`, result.Tool, err.Error())
	}
	return string(bytes)
}

func summarizeToolResults(results []tools.ExecutionResult) string {
	if len(results) == 0 {
		return "Tool execution completed."
	}
	return "Tool execution completed."
}

func parseFallbackToolCall(content string) (ToolCall, bool) {
	var payload struct {
		Action    string          `json:"action"`
		Tool      string          `json:"tool"`
		Arguments json.RawMessage `json:"arguments"`
	}
	candidate := extractJSONObject(content)
	if candidate == "" {
		return ToolCall{}, false
	}
	if err := json.Unmarshal([]byte(candidate), &payload); err != nil {
		return ToolCall{}, false
	}
	if payload.Action != "tool_call" || payload.Tool == "" {
		return ToolCall{}, false
	}
	if len(payload.Arguments) == 0 {
		payload.Arguments = json.RawMessage(`{}`)
	}
	return ToolCall{
		ID:   "fallback_call_1",
		Type: "function",
		Function: FunctionCall{
			Name:      payload.Tool,
			Arguments: string(payload.Arguments),
		},
	}, true
}

func extractJSONObject(content string) string {
	content = strings.TrimSpace(content)
	if strings.HasPrefix(content, "```") {
		content = strings.TrimPrefix(content, "```json")
		content = strings.TrimPrefix(content, "```")
		content = strings.TrimSuffix(content, "```")
		content = strings.TrimSpace(content)
	}
	start := strings.Index(content, "{")
	end := strings.LastIndex(content, "}")
	if start < 0 || end < start {
		return ""
	}
	return content[start : end+1]
}

func suffix(index int, total int) string {
	if index == total-1 {
		return ""
	}
	return " "
}

func normalizeBaseURL(baseURL string) string {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return "https://api.openai.com/v1"
	}
	return strings.TrimRight(baseURL, "/")
}

func messagesTextLength(messages []Message) int {
	total := 0
	for _, message := range messages {
		total += len(message.Content)
	}
	return total
}

func messagesToText(messages []Message) string {
	var builder strings.Builder
	for _, message := range messages {
		if message.Content == "" {
			continue
		}
		builder.WriteString(message.Role)
		builder.WriteString(": ")
		builder.WriteString(message.Content)
		builder.WriteString("\n")
	}
	return builder.String()
}

func estimateUsage(input string, output string) Usage {
	promptTokens := estimateTokens(input)
	completionTokens := estimateTokens(output)
	return Usage{
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		TotalTokens:      promptTokens + completionTokens,
		Estimated:        true,
	}
}

func estimateTokens(value string) int {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	return len([]rune(value))/4 + 1
}

func deterministicEmbedding(input string, dimensions int) []float64 {
	if dimensions <= 0 {
		dimensions = 1536
	}
	vector := make([]float64, dimensions)
	words := strings.Fields(strings.ToLower(input))
	if len(words) == 0 {
		words = []string{input}
	}
	for _, word := range words {
		sum := sha256.Sum256([]byte(word))
		for i := 0; i < len(sum); i += 2 {
			index := int(sum[i])<<8 + int(sum[i+1])
			index = index % dimensions
			sign := 1.0
			if sum[(i+1)%len(sum)]%2 == 0 {
				sign = -1
			}
			vector[index] += sign
		}
	}
	var norm float64
	for _, value := range vector {
		norm += value * value
	}
	if norm == 0 {
		return vector
	}
	norm = math.Sqrt(norm)
	for i := range vector {
		vector[i] = vector[i] / norm
	}
	return vector
}

func (u Usage) Valid() bool {
	return u.PromptTokens > 0 || u.CompletionTokens > 0 || u.TotalTokens > 0
}

func tokenPayload(payload map[string]any, usage Usage) map[string]any {
	payload["prompt_tokens"] = usage.PromptTokens
	payload["completion_tokens"] = usage.CompletionTokens
	payload["total_tokens"] = usage.TotalTokens
	payload["token_usage_estimated"] = usage.Estimated
	return payload
}

func retrievedMemoryPayload(memories []domain.RetrievedMemory) []map[string]any {
	items := make([]map[string]any, 0, len(memories))
	for _, memory := range memories {
		items = append(items, map[string]any{
			"id":              memory.Memory.ID,
			"kind":            memory.Memory.Kind,
			"content":         truncateText(memory.Memory.Content, 1200),
			"metadata":        memory.Memory.Metadata,
			"similarity":      memory.Similarity,
			"recency_boost":   memory.RecencyBoost,
			"score":           memory.Score,
			"conversation_id": memory.Memory.ConversationID,
			"run_id":          memory.Memory.RunID,
		})
	}
	return items
}

func retrievedChunkPayload(chunks []domain.RetrievedDocumentChunk) []map[string]any {
	items := make([]map[string]any, 0, len(chunks))
	for _, chunk := range chunks {
		items = append(items, map[string]any{
			"document_id":    chunk.Document.ID,
			"document_title": chunk.Document.Title,
			"chunk_id":       chunk.Chunk.ID,
			"chunk_index":    chunk.Chunk.ChunkIndex,
			"content":        truncateText(chunk.Chunk.Content, 1600),
			"metadata":       chunk.Chunk.Metadata,
			"similarity":     chunk.Similarity,
			"recency_boost":  chunk.RecencyBoost,
			"score":          chunk.Score,
		})
	}
	return items
}
