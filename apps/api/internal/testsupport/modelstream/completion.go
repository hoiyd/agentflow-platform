// Package modelstream renders completion fixtures as wire-level SSE.
// It is test support, not a runtime fallback for non-streaming providers.
package modelstream

import (
	"encoding/json"
	"fmt"
	"strings"
)

func Completion(response any) (string, error) {
	var data []byte
	var err error
	if text, ok := response.(string); ok {
		data = []byte(text)
	} else {
		data, err = json.Marshal(response)
	}
	if err != nil {
		return "", err
	}
	var decoded struct {
		Choices []struct {
			Message      map[string]any `json:"message"`
			FinishReason *string        `json:"finish_reason"`
		} `json:"choices"`
		Usage any `json:"usage"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return "", err
	}
	if len(decoded.Choices) != 1 {
		return "", fmt.Errorf("fixture must return one choice")
	}
	choice := decoded.Choices[0]
	var output strings.Builder
	frame := func(delta map[string]any, finish *string) {
		data, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}})
		fmt.Fprintf(&output, "data: %s\n\n", data)
	}
	text := map[string]any{}
	for _, key := range []string{"content", "reasoning_content", "refusal"} {
		if value, ok := choice.Message[key]; ok {
			text[key] = value
		}
	}
	frame(text, nil)
	if calls, ok := choice.Message["tool_calls"].([]any); ok {
		for index, raw := range calls {
			call := raw.(map[string]any)
			call["index"] = index
			frame(map[string]any{"tool_calls": []any{call}}, nil)
		}
	}
	frame(map[string]any{}, choice.FinishReason)
	if decoded.Usage != nil {
		data, _ := json.Marshal(map[string]any{"choices": []any{}, "usage": decoded.Usage})
		fmt.Fprintf(&output, "data: %s\n\n", data)
	}
	output.WriteString("data: [DONE]\n\n")
	return output.String(), nil
}
