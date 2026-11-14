package app

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"agentflow-platform/apps/api/internal/config"
	"agentflow-platform/apps/api/internal/testsupport/modelstream"
	"agentflow-platform/apps/api/internal/tool"
	"agentflow-platform/apps/api/internal/tool/policy"
)

func browserToolApprovalConfig(t *testing.T, cfg *config.Config) {
	t.Helper()
	tools, err := tool.LoadConfig(cfg.ToolConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	for i := range tools.SecurityPolicy.Rules {
		if tools.SecurityPolicy.Rules[i].Tool == "file_patch" {
			tools.SecurityPolicy.Rules[i].Action = policy.ActionApprovalRequired
		}
	}
	tools.SecurityPolicy.Rules = append(tools.SecurityPolicy.Rules, policy.Rule{ID: "browser-approval-calculator", Tool: "calculator", Action: policy.ActionApprovalRequired, Capability: tool.CalculatorTool().Descriptor.Security})
	if err = tool.SaveConfig(cfg.ToolConfigPath, tools); err != nil {
		t.Fatal(err)
	}
}

func (f *browserProvider) toolApprovalResponse(w http.ResponseWriter, input browserModelRequest, task string) bool {
	if !strings.Contains(task, "tool-approval-gate") {
		return false
	}
	available, search := false, false
	for _, definition := range input.Tools {
		available = available || definition.Function.Name == "file_patch"
		search = search || definition.Function.Name == "tool_search"
	}
	content, finish := "Plan: edit the managed report after explicit approval.", "stop"
	var calls []any
	if search && !available && input.Stream {
		content, finish = "", "tool_calls"
		calls = []any{map[string]any{"id": "approval-discovery", "type": "function", "function": map[string]string{"name": "tool_search", "arguments": `{"query":"file_write file_patch"}`}}}
	} else if available && input.Stream {
		path := "approval-report.md"
		for _, suffix := range []string{"single", "multi-agent", "bounded", "reject"} {
			if strings.Contains(task, "tool-approval-gate-"+suffix) {
				path = "approval-report-" + suffix + ".md"
			}
		}
		next := "file_write"
		args := map[string]any{"root": "output", "path": path, "content": "Original report.\n"}
		for position, message := range input.Messages {
			for offset, call := range message.ToolCalls {
				index := position + offset + 1
				if message.ReasoningContent == nil || *message.ReasoningContent != "APPROVAL_PROTOCOL_CANARY" || index >= len(input.Messages) || input.Messages[index].Role != "tool" || input.Messages[index].ToolCallID != call.ID {
					f.reject(w, "approval continuation lost reasoning or Tool-call identity")
					return true
				}
				var result tool.ExecutionResult
				if json.Unmarshal([]byte(input.Messages[index].Content), &result) != nil {
					f.reject(w, "approval fixture received unsuccessful Tool observation")
					return true
				}
				if result.Error != nil {
					if call.Function.Name == "file_patch" && string(result.Error.Code) == "filesystem_write_conflict" && strings.Contains(task, "prewrite-correction") {
						next = "file_read"
						args = map[string]any{"root": "output", "path": path}
						continue
					}
					f.reject(w, "approval fixture received unsuccessful Tool observation")
					return true
				}
				if call.Function.Name == "file_write" {
					next = "file_patch"
					args = map[string]any{"root": "output", "path": path, "expected_hash": fmt.Sprintf("%x", sha256.Sum256([]byte("Original report.\n"))), "old_text": "Original", "new_text": "Approved"}
				} else if call.Function.Name == "tool_search" {
					continue
				} else if call.Function.Name == "file_read" {
					value, ok := result.Result.(map[string]any)
					if !ok || value["content_hash"] == nil {
						f.reject(w, "correction lost file hash")
						return true
					}
					next = "file_patch"
					args = map[string]any{"root": "output", "path": path, "expected_hash": value["content_hash"], "old_text": "Original", "new_text": "Approved"}
				} else if call.Function.Name == "file_patch" {
					next = ""
				} else {
					f.reject(w, "unexpected approval fixture Tool")
					return true
				}
			}
		}
		if next != "" {
			content, finish = "", "tool_calls"
			encoded, _ := json.Marshal(args)
			calls = []any{map[string]any{"id": "approval-provider-call", "type": "function", "function": map[string]string{"name": next, "arguments": string(encoded)}}}
			if next == "file_patch" && strings.Contains(task, "mixed-approval-batch") {
				calls = append(calls, map[string]any{"id": "unreviewed-write", "type": "function", "function": map[string]string{"name": "file_write", "arguments": `{"root":"output","path":"unreviewed.md","content":"must not execute"}`}})
			}
		} else {
			content = "Report edit verified."
		}
	} else if strings.Contains(input.Messages[0].Content, "Decide stage") {
		content = `{"decision":"stop","reason":"report verified","final_answer":"Report edit verified."}`
	} else if strings.Contains(input.Messages[0].Content, "Finalizer collaboration role") {
		if !strings.Contains(task, "Report edit verified.") {
			f.reject(w, "finalizer did not receive successful worker evidence")
			return true
		}
		content = "Report edit verified."
	}
	reasoning := "APPROVAL_PROTOCOL_CANARY"
	completion := browserCompletion(content, finish, calls, &reasoning)
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
