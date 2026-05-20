package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/tools"
)

type Client struct {
	apiKey     string
	baseURL    string
	model      string
	httpClient *http.Client
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

func NewClient(apiKey string, baseURL string, model string) *Client {
	return &Client{
		apiKey:  strings.TrimSpace(apiKey),
		baseURL: normalizeBaseURL(baseURL),
		model:   strings.TrimSpace(model),
		httpClient: &http.Client{
			Timeout: 90 * time.Second,
		},
	}
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
			if _, err := c.streamMessages(ctx, messages, events); err != nil {
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
	return c.StreamAgentChatWithTools(ctx, "You are AgentFlow's Day 2 assistant. Use tools when they help.", history, latest, registry)
}

func (c *Client) StreamAgentChatWithTools(ctx context.Context, systemPrompt string, history []domain.Message, latest string, registry *tools.Registry) (<-chan StreamEvent, <-chan error) {
	events := make(chan StreamEvent)
	errs := make(chan error, 1)

	go func() {
		defer close(events)
		defer close(errs)

		if c.apiKey == "" {
			log.Printf("chat_fallback mode=local_no_api_key latest_len=%d enabled_tools=%q", len(latest), strings.Join(registry.EnabledNames(), ","))
			c.streamFallbackEvents(ctx, latest, events)
			return
		}

		if err := c.streamOpenAIWithTools(ctx, systemPrompt, history, registry, events); err != nil {
			errs <- err
		}
	}()

	return events, errs
}

func (c *Client) streamFallback(ctx context.Context, latest string, chunks chan<- string) {
	response := "Day 1 smoke test response: backend streaming is working. Add OPENAI_API_KEY in apps/api/.env to enable real OpenAI responses. You said: " + latest
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

func (c *Client) streamFallbackEvents(ctx context.Context, latest string, events chan<- StreamEvent) {
	response := "Day 2 smoke test response: backend streaming is working. Add OPENAI_API_KEY in apps/api/.env to enable model-directed tool calling. You said: " + latest
	words := strings.Split(response, " ")
	for i, word := range words {
		select {
		case <-ctx.Done():
			return
		case events <- StreamEvent{Type: "delta", Delta: word + suffix(i, len(words))}:
			time.Sleep(45 * time.Millisecond)
		}
	}
}

func (c *Client) streamOpenAIWithTools(ctx context.Context, systemPrompt string, history []domain.Message, registry *tools.Registry, events chan<- StreamEvent) error {
	enabledTools := registry.EnabledNames()
	messages := buildMessagesWithSystemPrompt(systemPrompt, history, enabledTools)
	decision, err := c.complete(ctx, map[string]any{
		"model":       c.model,
		"messages":    messages,
		"tools":       registry.Definitions(),
		"tool_choice": "auto",
		"temperature": 0.2,
	})
	if err != nil {
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
		return emitText(ctx, choice.Message.Content, events)
	}

	messages = append(messages, Message{
		Role:      "assistant",
		Content:   choice.Message.Content,
		ToolCalls: normalizeToolCalls(toolCalls),
	})

	results := make([]tools.ExecutionResult, 0, len(toolCalls))
	for _, call := range normalizeToolCalls(toolCalls) {
		log.Printf("tool_call_start id=%s tool=%s arguments=%q", call.ID, call.Function.Name, call.Function.Arguments)

		result := registry.Execute(ctx, call.Function.Name, json.RawMessage(call.Function.Arguments))
		results = append(results, result)
		resultText := marshalResult(result)
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

	emitted, err := c.streamMessages(ctx, messages, events)
	if err != nil {
		return err
	}
	if !emitted {
		log.Printf("chat_fallback mode=tool_summary_no_stream tool_count=%d", len(results))
		return emitText(ctx, summarizeToolResults(results), events)
	}
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

func (c *Client) streamMessages(ctx context.Context, messages []Message, events chan<- StreamEvent) (bool, error) {
	body := map[string]any{
		"model":       c.model,
		"messages":    messages,
		"stream":      true,
		"temperature": 0.4,
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return false, err
	}

	resp, err := c.doRequest(ctx, payload)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bytes, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return false, fmt.Errorf("openai-compatible stream failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(bytes)))
	}

	scanner := bufio.NewScanner(resp.Body)
	buf := make([]byte, 0, 1024*1024)
	scanner.Buffer(buf, 1024*1024)

	emitted := false
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			return emitted, nil
		}

		var event chatCompletionChunk
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			return false, err
		}
		for _, choice := range event.Choices {
			if choice.Delta.Content == "" {
				continue
			}
			emitted = true
			select {
			case <-ctx.Done():
				return false, ctx.Err()
			case events <- StreamEvent{Type: "delta", Delta: choice.Delta.Content}:
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return false, err
	}
	return false, errors.New("openai-compatible stream ended without [DONE]")
}

func (c *Client) doRequest(ctx context.Context, payload []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(payload))
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
}

type chatCompletionResponse struct {
	Choices []struct {
		Message Message `json:"message"`
	} `json:"choices"`
}

func buildMessages(history []domain.Message) []Message {
	return buildMessagesWithToolNames(history, []string{"calculator", "get_current_time", "mock_web_search"})
}

func buildMessagesWithToolNames(history []domain.Message, toolNames []string) []Message {
	return buildMessagesWithSystemPrompt("You are AgentFlow's Day 2 assistant. Use tools when they help.", history, toolNames)
}

func buildMessagesWithSystemPrompt(systemPrompt string, history []domain.Message, toolNames []string) []Message {
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
