package tool

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentflow-platform/apps/api/internal/tool/policy"
)

func TestDocumentedSandboxOperatorGrant(t *testing.T) {
	document, err := os.ReadFile("../../../../docs/operations/sandbox-execution.md")
	if err != nil {
		t.Fatal(err)
	}
	var grant json.RawMessage
	for index, block := range strings.Split(string(document), "```") {
		language, body, _ := strings.Cut(block, "\n")
		if index%2 == 0 || language != "json" {
			continue
		}
		var identity struct {
			Tool string `json:"tool"`
		}
		if json.Unmarshal([]byte(body), &identity) == nil && identity.Tool == "sandbox_command" {
			grant = json.RawMessage(body)
			break
		}
	}
	if grant == nil {
		t.Fatal("sandbox operator grant missing from the owning document")
	}
	data, err := json.Marshal(map[string]any{
		"enabled_tools":   []string{"sandbox_command"},
		"security_policy": map[string]any{"rules": []json.RawMessage{grant}},
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "tools.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	paths := map[string]string{"documentation": path}
	// An explicit local check is read-only and never changes operator policy.
	if operatorPath := os.Getenv("AGENTFLOW_TEST_TOOLS_CONFIG"); operatorPath != "" {
		paths["operator"] = operatorPath
	}
	for name, path := range paths {
		t.Run(name, func(t *testing.T) {
			binding := SandboxCommandTool(nil)
			manager, err := NewManager(path, binding)
			if err != nil {
				t.Fatalf("create tools manager: %v", err)
			}
			decision := policy.Evaluate(manager.config.SecurityPolicy, policy.Request{
				Tool: binding.Descriptor.Name, Declared: binding.Descriptor.Security,
				RequestedScope: binding.Descriptor.Security.Scope,
			})
			if !decision.Allowed || decision.Action != policy.ActionAllowAndLog {
				t.Fatalf("sandbox grant does not authorize the real descriptor: %+v", decision)
			}
		})
	}
}
