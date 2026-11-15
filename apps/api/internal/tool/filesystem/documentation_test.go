package filesystem

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentflow-platform/apps/api/internal/tool"
	"agentflow-platform/apps/api/internal/tool/policy"
)

// Missing/invalid examples and capability drift must fail before operators copy
// a rule that parses successfully but denies the actual Binding at execution.
func TestDocumentedFilesystemOperatorGrants(t *testing.T) {
	for _, example := range []struct{ document, name string }{
		{"scoped-filesystem.md", "file_list"},
		{"scoped-filesystem-writes.md", "file_write"},
	} {
		t.Run(example.name, func(t *testing.T) {
			document, err := os.ReadFile(filepath.Join("../../../../../docs/tools", example.document))
			if err != nil {
				t.Fatal(err)
			}
			var grant json.RawMessage
			for index, block := range strings.Split(string(document), "```") {
				language, body, _ := strings.Cut(block, "\n")
				if index%2 == 0 || language != "json" {
					continue
				}
				var identity struct{ Tool string }
				if json.Unmarshal([]byte(body), &identity) == nil && identity.Tool == example.name {
					grant = json.RawMessage(body)
					break
				}
			}
			if grant == nil {
				t.Fatal("operator grant missing from owning document")
			}
			data, err := json.Marshal(map[string]any{
				"enabled_tools":   []string{example.name},
				"security_policy": map[string]any{"rules": []json.RawMessage{grant}},
			})
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "tools.json")
			if err = os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			manager, err := tool.NewManager(path, (&Service{}).Bindings()...)
			if err != nil {
				t.Fatal(err)
			}
			catalog, err := manager.Catalog()
			if err != nil {
				t.Fatal(err)
			}
			binding, ok := catalog.Resolve(example.name)
			if !ok {
				t.Fatal("documented Tool was not enabled")
			}
			request := policy.Request{Tool: example.name, Declared: binding.Descriptor.Security, RequestedScope: binding.Descriptor.Security.Scope}
			decision := policy.Evaluate(catalog.SecurityPolicy(), request)
			if !decision.Allowed || decision.Action != policy.ActionAllowAndLog {
				t.Fatalf("documented grant does not authorize the real Binding: %+v", decision)
			}
			approvalPolicy := catalog.SecurityPolicy()
			approvalPolicy.Rules[0].Action = policy.ActionApprovalRequired
			decision = policy.Evaluate(approvalPolicy, request)
			if decision.Allowed || !policy.CanRequestApproval(decision) {
				t.Fatalf("changing action did not require approval: %+v", decision)
			}
		})
	}
}
