package openai

import (
	"strings"

	"agentflow-platform/apps/api/internal/inference/provider"
)

type toolCallDelta struct {
	Index    *int         `json:"index"`
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

// Stream fragments are transport state, never executable Tool requests. Bound
// aggregate continuation state as well as individual SSE frame sizes.
type streamedChoice struct {
	content      strings.Builder
	reasoning    strings.Builder
	hasReasoning bool
	calls        map[int]*streamedToolCall
	bytes        int
}

type streamedToolCall struct {
	ToolCall
	name      strings.Builder
	arguments strings.Builder
}

const maxToolStreamBytes = 1024 * 1024

func (s *streamedChoice) add(content string, reasoning *string, deltas []toolCallDelta) error {
	s.bytes += len(content)
	if reasoning != nil {
		s.bytes += len(*reasoning)
	}
	for _, delta := range deltas {
		s.bytes += len(delta.ID) + len(delta.Type) + len(delta.Function.Name) + len(delta.Function.Arguments)
	}
	if s.bytes > maxToolStreamBytes {
		return invalidResponseError("chat.stream", "model Tool stream exceeds aggregate size limit", nil)
	}
	s.content.WriteString(content)
	if reasoning != nil {
		s.hasReasoning = true
		s.reasoning.WriteString(*reasoning)
	}
	for _, delta := range deltas {
		if delta.Index == nil || *delta.Index < 0 || *delta.Index >= 1024 {
			return invalidResponseError("chat.stream", "invalid streamed Tool-call index", nil)
		}
		if s.calls == nil {
			s.calls = make(map[int]*streamedToolCall)
		}
		call := s.calls[*delta.Index]
		if call == nil {
			call = &streamedToolCall{}
			s.calls[*delta.Index] = call
		}
		if delta.ID != "" {
			if call.ID != "" && call.ID != delta.ID {
				return invalidResponseError("chat.stream", "streamed Tool-call identity changed", nil)
			}
			call.ID = delta.ID
		}
		if delta.Type != "" {
			if call.Type != "" && call.Type != delta.Type {
				return invalidResponseError("chat.stream", "streamed Tool-call type changed", nil)
			}
			call.Type = delta.Type
		}
		call.name.WriteString(delta.Function.Name)
		call.arguments.WriteString(delta.Function.Arguments)
	}
	return nil
}

func (s *streamedChoice) finish() (provider.ChatChoice, error) {
	choice := provider.ChatChoice{Content: s.content.String()}
	if s.hasReasoning {
		value := s.reasoning.String()
		choice.ReasoningContent = &value
	}
	for index := 0; index < len(s.calls); index++ {
		call, ok := s.calls[index]
		if !ok || strings.TrimSpace(call.ID) == "" || call.Type != "function" || strings.TrimSpace(call.name.String()) == "" {
			return choice, invalidResponseError("chat.stream", "incomplete streamed Tool-call identity", nil)
		}
		assembled := call.ToolCall
		assembled.Function = FunctionCall{Name: call.name.String(), Arguments: call.arguments.String()}
		choice.ToolCalls = append(choice.ToolCalls, assembled)
	}
	return choice, nil
}
