package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"agentflow-platform/apps/api/internal/testsupport/modelstream"
)

// Independent wire types make continuation omissions observable to fixtures.
type browserModelRequest struct {
	ResponseFormat map[string]any `json:"response_format"`
	Messages       []struct {
		Role             string  `json:"role"`
		Content          string  `json:"content"`
		ReasoningContent *string `json:"reasoning_content"`
		ToolCallID       string  `json:"tool_call_id"`
		ToolCalls        []struct {
			ID       string `json:"id"`
			Type     string `json:"type"`
			Function struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			} `json:"function"`
		} `json:"tool_calls"`
	} `json:"messages"`
	Tools []struct {
		Function struct {
			Name       string          `json:"name"`
			Parameters json.RawMessage `json:"parameters"`
		} `json:"function"`
	} `json:"tools"`
	Stream bool `json:"stream"`
}

func (f *browserProvider) clarificationResponse(w http.ResponseWriter, input browserModelRequest, task string) bool {
	if !strings.Contains(task, "clarification-gate") {
		return false
	}
	control := false
	for _, d := range input.Tools {
		control = control || d.Function.Name == "request_user_input"
	}
	for _, m := range input.Messages {
		if m.Role == "system" && strings.Contains(m.Content, "Worker collaboration role") {
			if control || !strings.Contains(task, "Engineering leads") {
				f.reject(w, "isolated worker did not receive durable clarification, or inherited question authority")
				return true
			}
		}
	}
	answerSeen := false
	for i, m := range input.Messages {
		for _, call := range m.ToolCalls {
			if call.Function.Name == "request_user_input" {
				if m.ReasoningContent == nil || *m.ReasoningContent != "CLARIFICATION_PROTOCOL_CANARY" || i+1 >= len(input.Messages) || input.Messages[i+1].Role != "tool" || input.Messages[i+1].ToolCallID != call.ID || !strings.Contains(input.Messages[i+1].Content, "Engineering leads") {
					f.reject(w, "clarification continuation lost identity, answer or reasoning protocol")
					return true
				}
				answerSeen = true
			}
		}
	}
	content := "Report for Engineering leads."
	reason := "stop"
	var calls []any
	var thinking *string
	legacy := strings.Contains(task, "clarification-gate-legacy")
	if input.Stream && control && !answerSeen && !legacy {
		content = ""
		reason = "tool_calls"
		v := "CLARIFICATION_PROTOCOL_CANARY"
		thinking = &v
		calls = []any{map[string]any{"id": "provider-question", "type": "function", "function": map[string]string{"name": "request_user_input", "arguments": `{"questions":[{"id":"audience","question":"Who is the audience?","options":["Engineers","Managers"]}]}`}}}
	}
	for _, m := range input.Messages {
		if m.Role == "system" && strings.Contains(m.Content, "Decide stage") {
			content = `{"decision":"stop","reason":"task complete","question":"","final_answer":"Report for Engineering leads."}`
		}
	}
	if legacy {
		content = "Ready to prepare the report."
		for _, m := range input.Messages {
			if m.Role == "system" && strings.Contains(m.Content, "Decide stage") {
				if strings.Contains(task, "Human input received:") || strings.Contains(task, "Report for Engineering leads.") {
					content = `{"decision":"stop","reason":"task complete","question":"","final_answer":"Report for Engineering leads."}`
				} else {
					content = `{"decision":"ask_user","reason":"missing audience","question":"Who is the audience?","final_answer":""}`
				}
			}
		}
		if strings.Contains(task, "Human input received:") && input.Stream {
			content = "Report for Engineering leads."
		}
	}
	if strings.Contains(task, "rejected-credential-question") {
		content = strings.ReplaceAll(content, "Who is the audience?", "What is your password?")
	}
	completion := browserCompletion(content, reason, calls, thinking)
	if input.Stream {
		stream, err := modelstream.Completion(completion)
		if err != nil {
			f.reject(w, err.Error())
			return true
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, stream)
	} else {
		json.NewEncoder(w).Encode(completion)
	}
	return true
}
