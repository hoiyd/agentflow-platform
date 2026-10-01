package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

func browserDisplayReasoning(round int) string {
	return fmt.Sprintf("DISPLAY_ONLY_REASONING round %d: inspect evidence with sk-fixtureDisplayCredential123456.", round)
}

func browserReasoningFrame(w http.ResponseWriter, delta map[string]any, finish any) {
	encoded, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}})
	fmt.Fprintf(w, "data: %s\n\n", encoded)
	w.(http.Flusher).Flush()
}

func (f *browserProvider) respondReasoning(w http.ResponseWriter, r *http.Request, task string, round int) {
	w.Header().Set("Content-Type", "text/event-stream")
	reasoning := browserDisplayReasoning(round)
	if strings.Contains(task, "reasoning-empty") {
		reasoning = ""
	}
	if strings.Contains(task, "reasoning-cap") {
		reasoning += strings.Repeat("解释", 4000)
	}
	// Split a credential across frames, interleave answer text, and choose Tools
	// only afterwards. This fixture tests the wire, not adapter's Go DTOs.
	split := len(reasoning) / 2
	if index := strings.Index(reasoning, "Credential"); index >= 0 {
		split = index
	}
	browserReasoningFrame(w, map[string]any{"reasoning_content": reasoning[:split]}, nil)
	if strings.Contains(task, "reasoning-cancel") {
		select {
		case <-r.Context().Done():
		case <-time.After(35 * time.Second):
			f.reject(w, "browser did not cancel the Run")
		}
		return
	}
	if strings.Contains(task, "reasoning-disconnect") {
		return
	}
	if strings.Contains(task, "reasoning-gate") {
		select {
		case <-f.gate:
		case <-r.Context().Done():
			return
		case <-time.After(35 * time.Second):
			f.reject(w, "reasoning receiving state not observed")
			return
		}
	}
	tools := strings.Contains(task, "reasoning-display") && round < 2
	if tools {
		browserReasoningFrame(w, map[string]any{"content": "Provisional tool commentary"}, nil)
	}
	browserReasoningFrame(w, map[string]any{"reasoning_content": reasoning[split:]}, nil)
	if tools {
		browserReasoningFrame(w, map[string]any{"tool_calls": []any{map[string]any{
			"index": 0, "id": fmt.Sprintf("display-call-%d", round), "type": "function",
			"function": map[string]string{"name": "calculator", "arguments": `{"expression":"1 + 1"}`},
		}}}, "tool_calls")
	} else {
		browserReasoningFrame(w, map[string]any{"content": "Evidence saved."}, "stop")
	}
	if strings.Contains(task, "reasoning-budget") {
		fmt.Fprint(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":1000000,\"total_tokens\":1000010}}\n\n")
	}
	fmt.Fprint(w, "data: [DONE]\n\n")
}
