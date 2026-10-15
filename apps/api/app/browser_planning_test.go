package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"agentflow-platform/apps/api/internal/testsupport/modelstream"
)

// The provider deliberately forges hidden write calls after loading a Skill.
// Only real Executor denials and filesystem observations satisfy continuation.
func (f *browserProvider) planningResponse(w http.ResponseWriter, input browserModelRequest, task string) bool {
	if !strings.Contains(task, "planning-gate") {
		return false
	}
	system := ""
	for _, message := range input.Messages {
		if message.Role == "system" {
			system += message.Content
		}
	}
	planOnly := strings.Contains(system, "READ-ONLY PLANNING CONTRACT")
	available := map[string]bool{}
	for _, definition := range input.Tools {
		available[definition.Function.Name] = true
	}
	if planOnly && (available["file_write"] || available["file_patch"] || available["update_task_state"] || available["sandbox_command"]) {
		f.reject(w, "planning exposed a mutable Tool schema")
		return true
	}
	content, reason := "Proposed report: read project/report.md; create output/report.md; verify the saved text against its source.", "stop"
	var calls []any
	if input.Stream && available["file_read"] {
		sequence := []struct{ name, args, want string }{
			{"skill_load", `{"name":"fixture-method"}`, `"name":"fixture-method"`},
			{"file_read", `{"root":"project","path":"report.md"}`, "retained technical evidence"},
			{"file_write", `{"root":"output","path":"report.md","content":"Unapproved change"}`, `"code":"security_policy_denied"`},
			{"update_task_state", `{"expected_version":0,"operations":[{"type":"set_goal","goal":"Injected mutation"}]}`, `"code":"security_policy_denied"`},
			{"sandbox_command", `{"args":["/bin/sh","-c","echo mutate"]}`, `"code":"security_policy_denied"`},
		}
		if !planOnly {
			sequence = []struct{ name, args, want string }{
				{"file_read", `{"root":"project","path":"report.md"}`, "retained technical evidence"},
				{"file_write", `{"root":"output","path":"report.md","content":"Report executed: retained technical evidence."}`, `"new_hash"`},
			}
			content = "Report executed: saved output/report.md."
		}
		index := 0
		for position, message := range input.Messages {
			for offset, call := range message.ToolCalls {
				next := position + offset + 1
				if index >= len(sequence) || call.Function.Name != sequence[index].name || next >= len(input.Messages) || input.Messages[next].ToolCallID != call.ID || !strings.Contains(input.Messages[next].Content, sequence[index].want) {
					f.reject(w, "planning continuation lost actual denial/source evidence")
					return true
				}
				index++
			}
		}
		if index < len(sequence) {
			if planOnly && index > 0 && !strings.Contains(task, "FIXTURE_SKILL_BODY") {
				f.reject(w, "activated Skill was not assembled into Context")
				return true
			}
			op := sequence[index]
			content, reason = "", "tool_calls"
			calls = []any{map[string]any{"id": fmt.Sprintf("planning-%d", index), "type": "function", "function": map[string]string{"name": op.name, "arguments": op.args}}}
		}
	} else if strings.Contains(system, "Decide stage") {
		content = `{"decision":"stop","reason":"planning finished","final_answer":"Proposed report: inspect project/report.md, save a sourced report and verify its contents."}`
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
