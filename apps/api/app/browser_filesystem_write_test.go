package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"agentflow-platform/apps/api/internal/testsupport/modelstream"
)

func (f *browserProvider) filesystemWriteResponse(w http.ResponseWriter, input browserModelRequest, task string) bool {
	if !strings.Contains(task, "filesystem-write-") {
		return false
	}
	available := map[string]bool{}
	for _, definition := range input.Tools {
		available[definition.Function.Name] = true
	}
	denied := strings.Contains(task, "filesystem-write-denied-gate")
	if denied && available["file_write"] {
		f.reject(w, "Skill unexpectedly granted write authority")
		return true
	}
	content, reason := "Report saved and verified.", "stop"
	if denied {
		content = "Skill cannot grant file write access."
	}
	var calls []any
	if input.Stream && available["file_read"] {
		path := "report.md"
		for i := range 3 {
			if strings.Contains(task, fmt.Sprintf("filesystem-write-gate-%d", i)) {
				path = fmt.Sprintf("report-%d.md", i)
			}
		}
		hash := ""
		for _, message := range input.Messages {
			if message.Role == "tool" {
				var result struct {
					Result struct {
						NewHash string `json:"new_hash"`
					} `json:"result"`
				}
				if json.Unmarshal([]byte(message.Content), &result) == nil && result.Result.NewHash != "" && hash == "" {
					hash = result.Result.NewHash
				}
			}
		}
		sequence := []struct{ name, args, want string }{
			{"skill_load", `{"name":"fixture-method"}`, `"fixture-method"`},
			{"file_write", fmt.Sprintf(`{"root":"output","path":%q,"content":"Original report evidence."}`, path), `"new_hash"`},
			{"file_read", fmt.Sprintf(`{"root":"output","path":%q}`, path), "Original report evidence."},
			{"file_write", fmt.Sprintf(`{"root":"output","path":%q,"content":"Unsafe overwrite"}`, path), `"code":"filesystem_write_conflict"`},
			{"file_write", `{"root":"project","path":"blocked.md","content":"Cannot grant writes"}`, `"code":"security_policy_denied"`},
			{"file_write", `{"root":"output","path":"../escape.md","content":"Escape"}`, `"code":"security_policy_denied"`},
			{"file_write", fmt.Sprintf(`{"root":"output","path":%q,"content":"Stale","expected_hash":%q}`, path, strings.Repeat("a", 64)), `"code":"filesystem_write_conflict"`},
			{"file_patch", fmt.Sprintf(`{"root":"output","path":%q,"expected_hash":%q,"old_text":"Original","new_text":"Verified"}`, path, hash), `"operation":"patch"`},
			{"file_read", fmt.Sprintf(`{"root":"output","path":%q}`, path), "Verified report evidence."},
		}
		if denied {
			sequence = sequence[:1]
		}
		index := 0
		for position, message := range input.Messages {
			for offset, call := range message.ToolCalls {
				next := position + offset + 1
				if index >= len(sequence) || call.Function.Name != sequence[index].name || next >= len(input.Messages) || input.Messages[next].Role != "tool" || input.Messages[next].ToolCallID != call.ID || !strings.Contains(input.Messages[next].Content, sequence[index].want) {
					f.reject(w, "filesystem write continuation lost identity or effect evidence")
					return true
				}
				index++
			}
		}
		if index < len(sequence) {
			operation := sequence[index]
			if !available[operation.name] {
				f.reject(w, "required filesystem write definition missing")
				return true
			}
			content, reason = "", "tool_calls"
			calls = []any{map[string]any{"id": fmt.Sprintf("filesystem-write-%d", index), "type": "function", "function": map[string]string{"name": operation.name, "arguments": operation.args}}}
		}
	} else if input.Stream {
		content = "Plan: save and verify a bounded report using the owned Agent."
	} else if strings.Contains(input.Messages[0].Content, "Decide stage") {
		content = `{"decision":"stop","reason":"report verified","final_answer":"Report saved and verified."}`
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
		json.NewEncoder(w).Encode(completion)
	}
	return true
}
