package tokenization

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type nativeBackend struct {
	baseURL string
	apiKey  string
	client  *http.Client
}

type backendProps struct {
	BuildInfo    string `json:"build_info"`
	ChatTemplate string `json:"chat_template"`
	BOSToken     string `json:"bos_token"`
	EOSToken     string `json:"eos_token"`
}

func (b nativeBackend) props(ctx context.Context) (backendProps, error) {
	var props backendProps
	err := b.request(ctx, http.MethodGet, "/props", nil, &props)
	return props, err
}

func (b nativeBackend) inputTokens(ctx context.Context, payload []byte) (int, error) {
	var result struct {
		InputTokens int `json:"input_tokens"`
	}
	if err := b.request(ctx, http.MethodPost, "/v1/chat/completions/input_tokens", payload, &result); err != nil {
		return 0, err
	}
	if result.InputTokens <= 0 {
		return 0, errors.New("backend returned no input-token count")
	}
	return result.InputTokens, nil
}

func (b nativeBackend) applyTemplate(ctx context.Context, payload []byte) (string, error) {
	var result struct {
		Prompt string `json:"prompt"`
	}
	if err := b.request(ctx, http.MethodPost, "/apply-template", payload, &result); err != nil {
		return "", err
	}
	if result.Prompt == "" {
		return "", errors.New("backend returned an empty chat-template prompt")
	}
	return result.Prompt, nil
}

func (b nativeBackend) tokenize(ctx context.Context, content string, addSpecial bool) (int, error) {
	payload, err := json.Marshal(map[string]any{"content": content, "add_special": addSpecial, "parse_special": true})
	if err != nil {
		return 0, err
	}
	var result struct {
		Tokens []json.RawMessage `json:"tokens"`
	}
	if err := b.request(ctx, http.MethodPost, "/tokenize", payload, &result); err != nil {
		return 0, err
	}
	if result.Tokens == nil {
		return 0, errors.New("backend returned no token list")
	}
	return len(result.Tokens), nil
}

func (b nativeBackend) completionUsage(ctx context.Context, payload []byte) (*int, error) {
	var result struct {
		Choices []json.RawMessage `json:"choices"`
		Usage   *struct {
			PromptTokens int `json:"prompt_tokens"`
		} `json:"usage"`
	}
	if err := b.request(ctx, http.MethodPost, "/v1/chat/completions", payload, &result); err != nil {
		return nil, err
	}
	if len(result.Choices) == 0 {
		return nil, errors.New("backend completion returned no choices")
	}
	if result.Usage == nil || result.Usage.PromptTokens <= 0 {
		return nil, nil
	}
	return &result.Usage.PromptTokens, nil
}

func (b nativeBackend) request(ctx context.Context, method, path string, payload []byte, result any) error {
	request, err := http.NewRequestWithContext(ctx, method, b.baseURL+path, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if b.apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+b.apiKey)
	}
	response, err := b.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode/100 != 2 {
		return fmt.Errorf("%s returned HTTP %d", path, response.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 8<<20)).Decode(result); err != nil {
		return fmt.Errorf("decode %s response: %w", path, err)
	}
	return nil
}

func requestContent(payload []byte) (string, error) {
	var request struct {
		Messages []struct {
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(payload, &request); err != nil {
		return "", err
	}
	if len(request.Messages) == 0 {
		return "", errors.New("request has no messages")
	}
	var content strings.Builder
	for index, message := range request.Messages {
		if index > 0 {
			content.WriteByte('\n')
		}
		content.WriteString(message.Content)
	}
	return content.String(), nil
}

func removeTools(payload []byte) ([]byte, error) {
	var request map[string]json.RawMessage
	if err := json.Unmarshal(payload, &request); err != nil {
		return nil, err
	}
	if len(request["tools"]) == 0 {
		return nil, errors.New("captured request has no Tools")
	}
	delete(request, "tools")
	delete(request, "tool_choice")
	return json.Marshal(request)
}
