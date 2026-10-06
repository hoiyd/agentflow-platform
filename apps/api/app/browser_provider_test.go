package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"agentflow-platform/apps/api/internal/testsupport/modelstream"
)

type browserProvider struct {
	mu       sync.Mutex
	requests int
	failures []string
	gate     chan struct{}
	once     sync.Once
}

func newBrowserProvider() *browserProvider { return &browserProvider{gate: make(chan struct{})} }

func (f *browserProvider) contracts(w http.ResponseWriter, _ *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	_ = json.NewEncoder(w).Encode(map[string]any{"requests": f.requests, "failures": f.failures})
}

func (f *browserProvider) release(w http.ResponseWriter, _ *http.Request) {
	f.once.Do(func() { close(f.gate) })
	w.WriteHeader(http.StatusNoContent)
}

func (f *browserProvider) reject(w http.ResponseWriter, message string) {
	f.mu.Lock()
	f.failures = append(f.failures, message)
	f.mu.Unlock()
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"message": message, "code": "invalid_request_error"}})
}

func (f *browserProvider) respond(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/rerank" {
		var input struct {
			Query      string   `json:"query"`
			Texts      []string `json:"texts"`
			RawScores  *bool    `json:"raw_scores"`
			ReturnText *bool    `json:"return_text"`
			Truncate   *bool    `json:"truncate"`
		}
		if r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(&input) != nil || input.Query == "" || len(input.Texts) == 0 || input.RawScores == nil || *input.RawScores || input.ReturnText == nil || *input.ReturnText || input.Truncate == nil || *input.Truncate {
			f.reject(w, "invalid cross-encoder wire contract")
			return
		}
		if strings.Contains(input.Query, "reranker-failure") {
			http.Error(w, "PRIVATE_RERANKER_RESPONSE", http.StatusServiceUnavailable)
			return
		}
		if strings.Contains(input.Query, "reranker-timeout") {
			<-r.Context().Done()
			return
		}
		scores := []map[string]any{}
		for index := range input.Texts {
			scores = append(scores, map[string]any{"index": index, "score": 0.9})
		}
		_ = json.NewEncoder(w).Encode(scores)
		return
	}
	if r.URL.Path == "/embeddings" {
		vector := make([]float64, 1536)
		vector[0] = 1
		_ = json.NewEncoder(w).Encode(map[string]any{"model": "fixture-embedding", "data": []any{map[string]any{"embedding": vector}}})
		return
	}
	var input struct {
		// Independent wire schema: reusing provider.Message here would let an
		// accidental JSON tag rename change the client and fixture together.
		Messages []struct {
			Role             string  `json:"role"`
			Content          string  `json:"content"`
			ReasoningContent *string `json:"reasoning_content"`
			ToolCallID       string  `json:"tool_call_id"`
			ToolCalls        []struct {
				ID string `json:"id"`
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
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil || len(input.Messages) == 0 {
		f.reject(w, "invalid fixture request")
		return
	}
	f.mu.Lock()
	f.requests++
	f.mu.Unlock()
	if strings.HasPrefix(input.Messages[0].Content, "Generate a concise conversation title.") {
		_ = json.NewEncoder(w).Encode(browserCompletion("Fixture conversation", "stop", nil, nil))
		return
	}
	task, observations := "", 0
	for _, message := range input.Messages {
		if message.Role == "user" {
			task += message.Content
		}
		if message.Role == "tool" {
			observations++
		}
	}
	if strings.Contains(task, "provider-failure") {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"message": "Fixture rejected the model request", "code": "invalid_request_error"}})
		return
	}
	if input.Stream && strings.Contains(task, "reasoning-") {
		previous := 0
		for _, message := range input.Messages {
			if message.Role != "assistant" || len(message.ToolCalls) == 0 {
				continue
			}
			if message.ReasoningContent == nil || *message.ReasoningContent != browserDisplayReasoning(previous) {
				f.reject(w, "display redaction mutated required continuation")
				return
			}
			previous++
		}
		if previous != observations {
			f.reject(w, "reasoning round identity lost")
			return
		}
		f.respondReasoning(w, r, task, observations)
		return
	}
	if strings.Contains(task, "stream-gate") {
		if !input.Stream {
			f.reject(w, "answer must use streaming")
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"First token\"},\"finish_reason\":null}]}\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-f.gate:
		case <-r.Context().Done():
			return
		case <-time.After(35 * time.Second):
			f.reject(w, "browser did not observe the first token")
			return
		}
		stream, _ := modelstream.Completion(browserCompletion(" then finished.", "stop", nil, nil))
		fmt.Fprint(w, stream)
		return
	}
	content, reason := "Evidence saved.", "stop"
	var calls []any
	var reasoning *string
	if input.Stream && strings.Contains(task, "security-gate") {
		platformMessages := 0
		for _, message := range input.Messages {
			if message.Role == "system" && strings.HasPrefix(message.Content, "AgentFlow platform rules:") {
				platformMessages++
				if strings.Contains(message.Content, "IGNORE_PLATFORM_FIXTURE") {
					f.reject(w, "editable prompt merged into fixed platform instructions")
					return
				}
			}
		}
		if platformMessages != 1 {
			f.reject(w, "separate platform instructions missing or duplicated")
			return
		}
		last := input.Messages[len(input.Messages)-1].Content
		if observations == 1 && !strings.Contains(last, `"version":1`) ||
			observations == 2 && !strings.Contains(last, "private_context_egress_denied") ||
			observations == 3 && !strings.Contains(last, `"value":2`) {
			f.reject(w, "private data boundary or local continuation failed")
			return
		}
		if observations < 3 {
			name, args := "update_task_state", `{"expected_version":0,"operations":[{"type":"set_goal","goal":"PRIVATE_BROWSER_FIXTURE_FACT"}]}`
			if observations == 1 {
				// A paraphrased query is not required to contain a private canary.
				// Use public text so a failing guard cannot leak fixture data.
				name, args = "web_search", `{"query":"public release notes"}`
			}
			if observations == 2 {
				name, args = "calculator", `{"expression":"1 + 1"}`
			}
			available := false
			for _, definition := range input.Tools {
				if definition.Function.Name == name {
					available = true
				}
			}
			if !available {
				f.reject(w, "security gate Tool missing: "+name)
				return
			}
			content, reason = "Applying the requested action.", "tool_calls"
			calls = []any{map[string]any{"id": fmt.Sprintf("security-call-%d", observations), "type": "function", "function": map[string]string{"name": name, "arguments": args}}}
		} else {
			content = "Private data stayed local. Calculation: 2."
		}
	}
	if input.Stream && strings.Contains(task, "sandbox-gate") {
		if observations == 0 {
			available := false
			for _, definition := range input.Tools {
				if definition.Function.Name == "sandbox_command" {
					available = true
					if !strings.Contains(string(definition.Function.Parameters), "prefixItems") || !strings.Contains(string(definition.Function.Parameters), "/usr/bin/python3") {
						f.reject(w, "sandbox executable contract missing from model request")
						return
					}
				}
			}
			if !available {
				f.reject(w, "sandbox tool missing from frozen model definitions")
				return
			}
			content, reason = "Running scratch command.", "tool_calls"
			calls = []any{map[string]any{"id": "sandbox-browser-call", "type": "function", "function": map[string]string{"name": "sandbox_command", "arguments": `{"args":["python","-c","print(sum(range(10)))"]}`}}}
		} else if observations == 1 {
			last := input.Messages[len(input.Messages)-1].Content
			if !strings.Contains(last, `"code":"invalid_arguments"`) || !strings.Contains(last, `"path":"/args/0"`) {
				f.reject(w, "invalid executable did not reach safe model correction")
				return
			}
			content, reason = "Correcting the executable path.", "tool_calls"
			calls = []any{map[string]any{"id": "sandbox-browser-corrected", "type": "function", "function": map[string]string{"name": "sandbox_command", "arguments": `{"args":["/usr/bin/python3","-c","print(sum(range(10)))"]}`}}}
		} else {
			last := input.Messages[len(input.Messages)-1].Content
			if !strings.Contains(last, "sandbox browser receipt") || !strings.Contains(last, `"cleanup_confirmed":true`) {
				f.reject(w, "sandbox receipt lost before answer continuation")
				return
			}
			content = "Sandbox receipt saved."
		}
	}
	if input.Stream && strings.Contains(task, "owner-tools") && observations < 4 {
		for _, definition := range input.Tools {
			if definition.Function.Name != "get_current_time" {
				continue
			}
			zones := []string{"UTC", "Asia/Shanghai", "Europe/London", "America/New_York"}
			args, _ := json.Marshal(map[string]string{"timezone": zones[observations]})
			content, reason = "Checking time zones.", "tool_calls"
			calls = []any{map[string]any{"id": fmt.Sprintf("owner-call-%d", observations), "type": "function", "function": map[string]string{"name": "get_current_time", "arguments": string(args)}}}
			break
		}
	}
	if len(input.Tools) > 0 && strings.Contains(task, "state-protocol") {
		worker := strings.Contains(input.Messages[0].Content, "Worker collaboration role")
		if !input.Stream {
			f.reject(w, "tool request must use streaming")
			return
		}
		// Validate actual wire messages, not an in-memory response round-trip.
		previous := 0
		pending := map[string]bool{}
		for _, message := range input.Messages {
			if message.Role == "assistant" && len(message.ToolCalls) > 0 {
				want := fmt.Sprintf("FIXTURE_PRIVATE_REASONING_%d", previous)
				if message.ReasoningContent == nil || *message.ReasoningContent != want {
					f.reject(w, "required reasoning_content lost on tool continuation")
					return
				}
				previous++
				for _, call := range message.ToolCalls {
					pending[call.ID] = true
				}
			}
			if message.Role == "tool" {
				if !pending[message.ToolCallID] {
					f.reject(w, "tool observation lost its call identity")
					return
				}
				delete(pending, message.ToolCallID)
			}
		}
		if previous != observations || len(pending) != 0 {
			f.reject(w, "unpaired tool messages")
			return
		}
		all := fmt.Sprint(input.Messages)
		if observations > 0 && !strings.Contains(all, "FIXTURE_SKILL_BODY") {
			f.reject(w, "skill body was not delivered")
			return
		}
		last := input.Messages[len(input.Messages)-1].Content
		errorPath := "/operations/1/details"
		if worker {
			errorPath = "/details"
			if strings.Contains(all, "<task_state ") {
				f.reject(w, "isolated Worker received conversation Task State")
				return
			}
			for _, definition := range input.Tools {
				if definition.Function.Name == "update_task_state" {
					f.reject(w, "isolated Worker gained conversation write authority")
					return
				}
			}
		}
		if observations == 2 && !strings.Contains(last, errorPath) {
			f.reject(w, "invalid arguments did not reach model correction")
			return
		}
		resultMarker := `"version":1`
		if worker {
			resultMarker = `"value":2`
		}
		if observations == 3 && !strings.Contains(last, resultMarker) {
			f.reject(w, "real guarded write failed")
			return
		}
		if observations < 3 {
			name, args := "skill_load", `{"name":"fixture-method"}`
			if observations == 1 {
				name, args = "update_task_state", `{"expected_version":0,"operations":[{"type":"set_goal","goal":"bad write"},{"type":"upsert_task","details":"wrong level"}]}`
			}
			if observations == 2 {
				name, args = "update_task_state", `{"expected_version":0,"operations":[{"type":"set_goal","goal":"Preserve evidence"},{"type":"upsert_task","task":{"id":"evidence","title":"Check evidence","details":"Exact durable fact","status":"completed"}}]}`
			}
			if worker && observations == 1 {
				name, args = "calculator", `{"expression":"1 + 1","details":"wrong level"}`
			}
			if worker && observations == 2 {
				name, args = "calculator", `{"expression":"1 + 1"}`
			}
			available := false
			for _, definition := range input.Tools {
				if definition.Function.Name == name {
					available = true
				}
			}
			if !available {
				f.reject(w, "required tool missing from actual model definitions: "+name)
				return
			}
			value := fmt.Sprintf("FIXTURE_PRIVATE_REASONING_%d", observations)
			reasoning = &value
			content, reason = "Provisional tool commentary", "tool_calls"
			calls = []any{map[string]any{"id": fmt.Sprintf("fixture-call-%d", observations), "type": "function", "function": map[string]string{"name": name, "arguments": args}}}
		}
	}
	if strings.Contains(input.Messages[0].Content, "Decide stage") {
		content = `{"decision":"stop","reason":"evidence saved","final_answer":"Evidence saved."}`
	}
	response := browserCompletion(content, reason, calls, reasoning)
	if strings.Contains(task, "usage-breakdown") {
		usage := map[string]any{"prompt_tokens": 50, "completion_tokens": 10, "total_tokens": 60}
		if !strings.Contains(task, "usage-unknown") {
			cached := 30
			if strings.Contains(task, "usage-zero") {
				cached = 0
			}
			if strings.Contains(task, "usage-invalid") {
				cached = 51
			}
			usage["prompt_tokens_details"] = map[string]any{"cached_tokens": cached}
			usage["completion_tokens_details"] = map[string]any{"reasoning_tokens": 4}
		}
		response["usage"] = usage
	}
	if input.Stream {
		stream, err := modelstream.Completion(response)
		if err != nil {
			f.reject(w, err.Error())
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, stream)
	} else {
		_ = json.NewEncoder(w).Encode(response)
	}
}

func browserCompletion(content, reason string, calls []any, reasoning *string) map[string]any {
	message := map[string]any{"role": "assistant", "content": content}
	if len(calls) > 0 {
		message["tool_calls"] = calls
	}
	if reasoning != nil {
		message["reasoning_content"] = *reasoning
	}
	return map[string]any{"choices": []any{map[string]any{"message": message, "finish_reason": reason}}, "usage": map[string]int{"prompt_tokens": 50, "completion_tokens": 10, "total_tokens": 60}}
}
