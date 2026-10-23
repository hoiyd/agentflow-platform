package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"agentflow-platform/apps/api/internal/testsupport/modelstream"
)

// A nonexistent source exercises the real production resolver without making
// CI contact the public web. Successful HTTPS reads have their own backend gate.
func (f *browserProvider) webFetchResponse(w http.ResponseWriter, input browserModelRequest, task string) bool {
	if !strings.Contains(task, "web-fetch-boundary") {
		return false
	}
	available, observations := false, 0
	for _, definition := range input.Tools {
		available = available || definition.Function.Name == "web_fetch"
	}
	for position, message := range input.Messages {
		for _, call := range message.ToolCalls {
			if call.Function.Name != "web_fetch" {
				continue
			}
			next := position + 1
			if next >= len(input.Messages) || input.Messages[next].Role != "tool" || input.Messages[next].ToolCallID != call.ID || !strings.Contains(input.Messages[next].Content, "security_scope_invalid") {
				f.reject(w, "web fetch lost scoped denial or call identity")
				return true
			}
			observations++
		}
	}
	content, reason := "Web fetch boundary confirmed; no article body was available.", "stop"
	var calls []any
	if input.Stream && available && observations == 0 {
		content, reason = "", "tool_calls"
		calls = []any{map[string]any{"id": "fetch-unknown-source", "type": "function", "function": map[string]string{"name": "web_fetch", "arguments": `{"source_id":"W999"}`}}}
	} else if input.Stream && !available {
		content = "Plan: verify the public source reference without inventing article evidence."
	} else if !input.Stream && strings.Contains(input.Messages[0].Content, "Decide stage") {
		content = `{"decision":"stop","reason":"source boundary verified","final_answer":"Web fetch boundary confirmed; no article body was available."}`
	}
	completion := browserCompletion(content, reason, calls, nil)
	if input.Stream {
		stream, err := modelstream.Completion(completion)
		if err != nil {
			f.reject(w, err.Error())
			return true
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, stream)
	} else {
		_ = json.NewEncoder(w).Encode(completion)
	}
	return true
}
