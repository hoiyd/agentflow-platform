package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/domain"
)

type Client struct {
	apiKey     string
	baseURL    string
	model      string
	httpClient *http.Client
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
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
			c.streamFallback(ctx, latest, chunks)
			return
		}

		if err := c.streamOpenAI(ctx, history, chunks); err != nil {
			errs <- err
		}
	}()

	return chunks, errs
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

func (c *Client) streamOpenAI(ctx context.Context, history []domain.Message, chunks chan<- string) error {
	messages := []Message{
		{
			Role:    "system",
			Content: "You are AgentFlow's Day 1 assistant. Be concise, practical, and helpful.",
		},
	}
	for _, item := range history {
		if item.Role == "user" || item.Role == "assistant" {
			messages = append(messages, Message{Role: item.Role, Content: item.Content})
		}
	}

	body := map[string]any{
		"model":       c.model,
		"messages":    messages,
		"stream":      true,
		"temperature": 0.4,
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("HTTP-Referer", "http://localhost:3000")
	req.Header.Set("X-Title", "AgentFlow Platform")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bytes, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("openai request failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(bytes)))
	}

	scanner := bufio.NewScanner(resp.Body)
	buf := make([]byte, 0, 1024*1024)
	scanner.Buffer(buf, 1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			return nil
		}

		var event chatCompletionChunk
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			return err
		}
		for _, choice := range event.Choices {
			if choice.Delta.Content == "" {
				continue
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case chunks <- choice.Delta.Content:
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return err
	}
	return errors.New("openai stream ended without [DONE]")
}

type chatCompletionChunk struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
	} `json:"choices"`
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
