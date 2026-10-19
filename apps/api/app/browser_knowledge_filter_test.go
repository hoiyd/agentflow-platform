package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/testsupport/modelstream"
)

// The fixture checks the actual outgoing prompt and Tool observations. It does
// not implement filters: real retrieval/Bindings must keep the decoy out.
func (f *browserProvider) knowledgeFilterResponse(w http.ResponseWriter, input browserModelRequest, task string) bool {
	if !strings.Contains(task, "rag-filter-gate") {
		return false
	}
	for _, message := range input.Messages {
		if strings.Contains(message.Content, "FORBIDDEN_SCOPE_CONTENT") {
			f.reject(w, "ineligible Knowledge reached model input")
			return true
		}
	}
	content, reason := "Scope evidence is sufficient [S1].", "stop"
	var calls []any
	hasSearch := false
	for _, definition := range input.Tools {
		hasSearch = hasSearch || definition.Function.Name == "knowledge_search"
	}
	observations := []string{}
	for _, message := range input.Messages {
		if message.Role == "tool" {
			observations = append(observations, message.Content)
		}
	}
	if hasSearch && len(observations) < 2 {
		name, args := "knowledge_search", `{"query":"ROLLBACK-742","max_results":5}`
		if len(observations) == 1 {
			var envelope struct {
				Result domain.KnowledgeToolSearchResult `json:"result"`
			}
			if json.Unmarshal([]byte(observations[0]), &envelope) != nil || len(envelope.Result.Items) != 1 {
				f.reject(w, "Knowledge filter did not isolate one issued reference")
				return true
			}
			name, args = "knowledge_read", fmt.Sprintf(`{"reference":%q,"limit":256}`, envelope.Result.Items[0].Reference)
		}
		content, reason = "", "tool_calls"
		calls = []any{map[string]any{"id": fmt.Sprintf("scope-%d", len(observations)), "type": "function", "function": map[string]string{"name": name, "arguments": args}}}
	}
	if len(input.Messages) > 0 && strings.Contains(input.Messages[0].Content, "Decide stage") {
		content = `{"decision":"stop","reason":"scoped evidence read","final_answer":"Scope evidence is sufficient [S1]."}`
	}
	if len(input.Messages) > 0 && strings.Contains(input.Messages[0].Content, "Router collaboration role") {
		matches := regexp.MustCompile("`(agent_[a-z0-9]+)`").FindAllStringSubmatch(task, -1)
		if len(matches) != 1 {
			f.reject(w, "scope fixture expects one owned routing candidate")
			return true
		}
		content = fmt.Sprintf(`{"agent_id":%q,"reason":"scoped knowledge reader","confidence":0.99,"scores":[{"agent_id":%q,"score":95,"reason":"scoped knowledge reader"}]}`, matches[0][1], matches[0][1])
	}
	response := browserCompletion(content, reason, calls, nil)
	if input.Stream {
		stream, err := modelstream.Completion(response)
		if err != nil {
			f.reject(w, err.Error())
			return true
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, stream)
	} else {
		_ = json.NewEncoder(w).Encode(response)
	}
	return true
}
