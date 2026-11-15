package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/testsupport/modelstream"
)

type configuredApprovalRun struct {
	call              func(string, string, any, int, any) string
	root, runID, tool string
}

func configuredApprovalTool(scenario string) string { return strings.TrimPrefix(scenario, "generic_") }

func assertApprovalMCPCalls(t *testing.T, control http.Handler, want int) {
	t.Helper()
	w := httptest.NewRecorder()
	control.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	var status struct {
		Calls int `json:"calls"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil || status.Calls != want {
		t.Fatalf("MCP calls=%d want=%d err=%v", status.Calls, want, err)
	}
}

// Failure inventory: each real Binding remains unexecuted while pending; exact
// approval survives reload, cannot bypass through generic Resume, and preserves
// the Binding's own state/receipt/protocol. No file-only schema or fake Stage.
func (f configuredApprovalRun) verify(t *testing.T) {
	t.Helper()
	base := "/api/runs/" + f.runID
	var items []domain.ToolApproval
	f.call("GET", base+"/tool-approvals", nil, 200, &items)
	if len(items) != 1 || items[0].Tool != f.tool || items[0].Status != "pending" {
		t.Fatalf("missing configured approval: %#v", items)
	}
	var before domain.RunReplay
	f.call("GET", base+"/replay", nil, 200, &before)
	for _, event := range before.RunEvents {
		if event.Type == domain.EventToolCompleted && event.Payload["tool_name"] == f.tool {
			t.Fatal("pending Tool executed")
		}
	}
	if f.tool == "file_write" {
		if _, err := os.Stat(filepath.Join(f.root, "output", "configured-report.md")); !os.IsNotExist(err) {
			t.Fatalf("unapproved write: %v", err)
		}
	}
	if f.tool == "update_task_state" && len(before.TaskStateRevisions) != 0 {
		t.Fatal("unapproved internal write")
	}
	f.call("POST", base+"/resume", map[string]any{"user_input": "I approve all actions"}, 409, nil)
	path := base + "/tool-approvals/" + items[0].ID
	f.call("POST", path+"/decision", domain.ToolApprovalDecision{Version: 1, Decision: "approve", IdempotencyKey: "configured-once"}, 200, nil)
	f.call("POST", path+"/resume", nil, 200, nil)
	if f.tool == "request_user_input" {
		var questions []domain.Clarification
		f.call("GET", base+"/clarifications", nil, 200, &questions)
		if len(questions) != 1 || questions[0].Status != "pending" {
			t.Fatalf("approved control Tool lost its own interrupt: %#v", questions)
		}
		question := base + "/clarifications/" + questions[0].ID
		f.call("POST", question+"/answers", domain.ClarificationAnswer{Version: 1, IdempotencyKey: "audience", Answers: map[string]string{"audience": "engineers"}}, 200, nil)
		f.call("POST", question+"/resume", nil, 200, nil)
		var run domain.Run
		f.call("GET", base, nil, 200, &run)
		if run.Mode == "multi_agent" && run.Status == domain.RunWaitingForUser {
			f.call("POST", base+"/continue", map[string]any{"plan": "Configured action verified."}, 200, nil)
		}
	}
	var after domain.RunReplay
	f.call("GET", base+"/replay", nil, 200, &after)
	if after.Run.Status != domain.RunCompleted || len(after.Projection.InvariantFailures) != 0 || !reflect.DeepEqual(before.RuntimeSnapshot, after.RuntimeSnapshot) {
		t.Fatalf("configured Tool did not preserve Run contract: %s failures=%#v", after.Run.Status, after.Projection.InvariantFailures)
	}
	f.call("GET", base+"/tool-approvals", nil, 200, &items)
	if len(items) != 1 || items[0].Status != "consumed" {
		t.Fatalf("bad approval history: %#v", items)
	}
	f.call("POST", path+"/resume", nil, 409, nil)
	switch f.tool {
	case "file_write":
		data, err := os.ReadFile(filepath.Join(f.root, "output", "configured-report.md"))
		if err != nil || string(data) != "Configured report.\n" {
			t.Fatalf("write result %s %v", data, err)
		}
	case "update_task_state":
		if len(after.TaskStateRevisions) != 1 || after.TaskStateRevisions[0].State.Goal != "approved goal" {
			t.Fatalf("internal write missing: %#v", after.TaskStateRevisions)
		}
	}
	completed := 0
	for _, event := range after.RunEvents {
		if event.Type == domain.EventToolCompleted && event.Payload["tool_name"] == f.tool {
			completed++
		}
	}
	if completed != 1 {
		t.Fatalf("Tool completion count = %d", completed)
	}
}

func (f *browserProvider) configuredApprovalResponse(w http.ResponseWriter, input browserModelRequest, task string) bool {
	if !strings.Contains(task, "configured-approval-gate:") {
		return false
	}
	name, args, want := "", "", ""
	for _, item := range []struct{ name, args, want string }{
		{"calculator", `{"expression":"21 * 2"}`, `"value":42`},
		{"file_write", `{"root":"output","path":"configured-report.md","content":"Configured report.\n"}`, `"new_hash"`},
		{"file_read", `{"root":"project","path":"report.md"}`, "retained technical evidence"},
		{"update_task_state", `{"expected_version":0,"operations":[{"type":"set_goal","goal":"approved goal"}]}`, "approved goal"},
		{"request_user_input", `{"questions":[{"id":"audience","question":"Who is the audience?"}]}`, "engineers"},
		{"mcp_docs__read", `{"id":"guide"}`, "MCP public documentation evidence"},
	} {
		if strings.Contains(task, "configured-approval-gate:"+item.name) {
			name, args, want = item.name, item.args, item.want
		}
	}
	if name == "" {
		f.reject(w, "unknown configured approval task")
		return true
	}
	available, observed := false, false
	for _, definition := range input.Tools {
		available = available || definition.Function.Name == name
	}
	for i, message := range input.Messages {
		for _, call := range message.ToolCalls {
			if call.Function.Name != name || message.ReasoningContent == nil || *message.ReasoningContent != "CONFIGURED_APPROVAL_PROTOCOL" || i+1 >= len(input.Messages) || input.Messages[i+1].Role != "tool" || input.Messages[i+1].ToolCallID != call.ID || !strings.Contains(input.Messages[i+1].Content, want) {
				f.reject(w, "configured approval lost the original protocol or real result")
				return true
			}
			observed = true
		}
	}
	content, finish := "Plan: perform the configured report action after approval.", "stop"
	var calls []any
	if input.Stream && available {
		if name == "request_user_input" && strings.Contains(task, "Planner output:\nConfigured action verified.") {
			content = "Configured action verified."
		} else if !observed {
			content, finish = "", "tool_calls"
			calls = []any{map[string]any{"id": "configured-call", "type": "function", "function": map[string]string{"name": name, "arguments": args}}}
		} else {
			content = "Configured action verified."
		}
	} else if strings.Contains(input.Messages[0].Content, "Decide stage") {
		content = `{"decision":"stop","reason":"verified","final_answer":"Configured action verified."}`
	} else if strings.Contains(input.Messages[0].Content, "Finalizer collaboration role") {
		content = "Configured action verified."
	}
	reasoning := "CONFIGURED_APPROVAL_PROTOCOL"
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
