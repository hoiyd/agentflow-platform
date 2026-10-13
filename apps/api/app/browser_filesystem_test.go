package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentflow-platform/apps/api/internal/config"
	"agentflow-platform/apps/api/internal/identity"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/testsupport/modelstream"
	"agentflow-platform/apps/api/internal/tool"
	"agentflow-platform/apps/api/internal/tool/filesystem"
	"agentflow-platform/apps/api/internal/tool/policy"
)

func browserFilesystemConfig(t *testing.T, cfg *config.Config, root string) {
	t.Helper()
	storage, err := store.NewPostgresStore(cfg.DatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Close()
	workspace, err := storage.DefaultWorkspace(t.Context(), identity.SuperUserID)
	if err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(root, "project")
	if err = os.Mkdir(project, 0700); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{"report.md": "Project report: retained technical evidence.\n", ".env": "SENSITIVE_FILE_CANARY", "../outside.txt": "OUTSIDE_ROOT_CANARY"} {
		if err = os.WriteFile(filepath.Join(project, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	cfg.FilesystemRootsPath = filepath.Join(root, "filesystem-roots.json")
	data, _ := json.Marshal(map[string]any{"roots": []filesystem.RootConfig{{ID: "project", WorkspaceID: workspace, Path: project}}})
	if err = os.WriteFile(cfg.FilesystemRootsPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	service, err := filesystem.New(cfg.FilesystemRootsPath, storage)
	if err != nil {
		t.Fatal(err)
	}
	tools := tool.DefaultConfig()
	for _, binding := range service.Bindings() {
		tools.EnabledTools = append(tools.EnabledTools, binding.Descriptor.Name)
		tools.SecurityPolicy.Rules = append(tools.SecurityPolicy.Rules, policy.Rule{ID: "browser-" + binding.Descriptor.Name, Tool: binding.Descriptor.Name, Action: policy.ActionAllowAndLog, Capability: binding.Descriptor.Security})
	}
	if err = tool.SaveConfig(cfg.ToolConfigPath, tools); err != nil {
		t.Fatal(err)
	}
}

// Accept only real Tool observations paired with their originating model call.
// Failure results must reach the model without revealing protected bytes.
func (f *browserProvider) filesystemResponse(w http.ResponseWriter, input browserModelRequest, task string) bool {
	if !strings.Contains(task, "filesystem-gate") {
		return false
	}
	available := map[string]bool{}
	for _, definition := range input.Tools {
		available[definition.Function.Name] = true
	}
	content, reason := "Filesystem evidence confirmed.", "stop"
	var calls []any
	if input.Stream && available["file_read"] {
		sequence := []struct{ name, args, want string }{
			{"file_list", `{}`, `"project"`},
			{"file_list", `{"root":"project","path":"."}`, `"report.md"`},
			{"file_search", `{"root":"project","path":".","query":"technical"}`, `"content_hash"`},
			{"file_read", `{"root":"project","path":"../outside.txt"}`, `"code":"security_policy_denied"`},
			{"file_read", `{"root":"project","path":".env"}`, `"code":"security_policy_denied"`},
			{"file_read", `{"root":"project","path":"report.md"}`, `retained technical evidence`},
		}
		index := 0
		for position, message := range input.Messages {
			for offset, call := range message.ToolCalls {
				next := position + offset + 1
				if index >= len(sequence) || call.Function.Name != sequence[index].name || next >= len(input.Messages) || input.Messages[next].Role != "tool" || input.Messages[next].ToolCallID != call.ID || !strings.Contains(input.Messages[next].Content, sequence[index].want) || strings.Contains(input.Messages[next].Content, "CANARY") {
					f.reject(w, "filesystem protocol lost call identity, boundary or read evidence")
					return true
				}
				index++
			}
		}
		if index < len(sequence) {
			operation := sequence[index]
			if !available[operation.name] {
				f.reject(w, "filesystem definition missing")
				return true
			}
			content, reason = "", "tool_calls"
			calls = []any{map[string]any{"id": fmt.Sprintf("filesystem-%d", index), "type": "function", "function": map[string]string{"name": operation.name, "arguments": operation.args}}}
		}
	} else if input.Stream {
		content = "Plan: locate and read the project report, then summarize the evidence."
	} else if strings.Contains(input.Messages[0].Content, "Decide stage") {
		content = `{"decision":"stop","reason":"file evidence captured","final_answer":"Filesystem evidence confirmed."}`
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
