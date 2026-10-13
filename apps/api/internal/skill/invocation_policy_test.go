package skill

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
	eventpkg "agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/testsupport/fixturestore"
	"agentflow-platform/apps/api/internal/tool"
)

func TestSkillInvocationPackageDeclarations(t *testing.T) {
	for _, tc := range []struct{ header, want string }{
		{"", ""}, {"disable-model-invocation: false", ""},
		{"disable-model-invocation: true", "explicit_only"},
		{"disable-model-invocation: \"false\"", "reject"},
		{"disable-model-invocation: null", "reject"},
		{"disable-model-invocation: [false]", "reject"},
		{"policy:\n  allow_implicit_invocation: false", "reject"},
	} {
		t.Run(tc.header, func(t *testing.T) {
			dir := packageDirectory(t, "policy-method", "name: policy-method\ndescription: Report method\n"+tc.header, "Keep verified facts.")
			catalog, err := LoadDirectories([]string{dir})
			if tc.want == "reject" {
				if err == nil {
					t.Fatal("malformed/unsupported restrictive policy silently allowed")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			encoded, _ := json.Marshal(catalog.List()[0])
			var metadata map[string]any
			json.Unmarshal(encoded, &metadata)
			if tc.want != "" && metadata["invocation_policy"] != tc.want {
				t.Fatalf("metadata=%s", encoded)
			}
			if !Check(CheckOptions{Directories: []string{dir}}).Compatible {
				t.Fatal("Checker disagrees with supported declaration")
			}
		})
	}
}

func TestSkillInvocationCodexPackagePolicy(t *testing.T) {
	for _, tc := range []struct{ content, want string }{
		{"policy:\n  allow_implicit_invocation: false", "explicit_only"},
		{"policy:\n  allow_implicit_invocation: true", ""},
		{"policy:\n  allow_implicit_invocation: \"false\"", "reject"},
		{"policy: null", "reject"},
		{"policy:\n  allow_implicit_invocation: null", "reject"},
		{"policy:\n  unknown: false", "reject"},
		{"policy:\n  allow_implicit_invocation: false\n  allow_implicit_invocation: true", "reject"},
		{"policy: [false]", "reject"},
		{"policy: [", "reject"},
		{"policy: {allow_implicit_invocation: TRUE}", "reject"},
		{"policy: {}\npolicy: {allow_implicit_invocation: false}", "reject"},
		{"dependencies:\n  tools:\n    - type: mcp\n      value: unknown", "reject"},
		{"policy: {allow_implicit_invocation: true}\n---\npolicy: {}", "reject"},
	} {
		t.Run(tc.content, func(t *testing.T) {
			dir := packageDirectory(t, "codex-method", "name: codex-method\ndescription: Evidence method", "Keep facts.")
			if err := os.Mkdir(filepath.Join(dir, "agents"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "agents", "openai.yaml"), []byte(tc.content), 0600); err != nil {
				t.Fatal(err)
			}
			catalog, err := LoadDirectories([]string{dir})
			if tc.want == "reject" {
				if err == nil {
					t.Fatal("unsafe optional policy accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if string(catalog.List()[0].InvocationPolicy) != tc.want {
				t.Fatalf("policy=%s", catalog.List()[0].InvocationPolicy)
			}
		})
	}
}

func TestSkillInvocationExplicitOnlyBindingEnforced(t *testing.T) {
	dir := packageDirectory(t, "policy-method", "name: policy-method\ndescription: Report method", "Do not grant Tools.")
	catalog, err := LoadDirectories([]string{dir})
	if err != nil {
		t.Fatal(err)
	}
	packages, err := catalog.Freeze([]string{"policy-method"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	storage := fixturestore.New()
	if _, err := storage.CreateAgent(domain.Agent{ID: "agent", Name: "Policy holder"}); err != nil {
		t.Fatal(err)
	}
	conversation, err := storage.CreateConversation("invocation")
	if err != nil {
		t.Fatal(err)
	}
	var agent domain.RuntimeAgentSnapshot
	if err := json.Unmarshal([]byte(`{"id":"agent","skills":["policy-method"],"skill_policies":{"policy-method":"explicit_only"}}`), &agent); err != nil {
		t.Fatal(err)
	}
	snapshot := domain.RuntimeSnapshot{SchemaVersion: domain.CurrentRuntimeSnapshotVersion, Agent: agent, Skills: packages, RunBudget: &domain.RuntimeRunBudget{}}
	run, err := storage.CreateRunWithContract("agent", conversation.ID, snapshot, nil)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(storage)
	bindings, err := tool.NewCatalog(service.ToolBindings()...)
	if err != nil {
		t.Fatal(err)
	}
	executor := tool.NewExecutor(bindings, tool.ExecutorOptions{Tracer: eventpkg.NewToolExecutionTracer(eventpkg.NewRecorder(storage), run.ID, "")})
	ctx := WithAgent(eventpkg.WithScope(t.Context(), eventpkg.Scope{RunID: run.ID, ConversationID: conversation.ID, TurnID: "turn"}), "agent", "Write a report")
	result := executor.Execute(ctx, tool.ExecutionRequest{CallID: "forged", Tool: LoadToolName, Arguments: json.RawMessage(`{"name":"policy-method"}`)})
	if result.Error == nil || string(result.Error.Code) != "skill_invocation_denied" {
		t.Fatalf("model bypass accepted: %+v", result)
	}
	active, err := service.Active(ctx, &snapshot)
	if err != nil || len(active) != 0 {
		t.Fatalf("denied method activated: %+v %v", active, err)
	}
	explicit := WithAgent(ctx, "agent", "/skill:policy-method Write a report")
	active, err = service.Active(explicit, &snapshot)
	if err != nil || len(active) != 1 {
		t.Fatalf("explicit invocation blocked: %+v %v", active, err)
	}
	result = executor.Execute(explicit, tool.ExecutionRequest{CallID: "confirmation", Tool: LoadToolName, Arguments: json.RawMessage(`{"name":"policy-method"}`)})
	if result.Error != nil {
		t.Fatal(result.Error)
	}
	encoded, _ := json.Marshal(result.Result)
	if !strings.Contains(string(encoded), `"already_loaded":true`) {
		t.Fatalf("confirmation=%s", encoded)
	}
	// A successful-looking receipt cannot originate an explicit-only activation.
	if _, err := storage.CreateRunEvent(domain.RunEvent{RunID: run.ID, Type: domain.EventToolCompleted, Payload: map[string]any{"tool_name": LoadToolName, "result": loadResult{SkillMetadata: Metadata(packages[0]), AgentID: "agent"}}}); err != nil {
		t.Fatal(err)
	}
	if active, err := service.Active(ctx, &snapshot); err != nil || len(active) != 0 {
		t.Fatalf("receipt forged invocation intent: %+v %v", active, err)
	}
	invalid := snapshot
	invalid.Agent.SkillPolicies = map[string]domain.SkillInvocationPolicy{"policy-method": "unknown"}
	if _, err := Bound(&invalid, "agent"); err == nil {
		t.Fatal("corrupt frozen policy accepted")
	}
	invalid = snapshot
	invalid.Skills = append([]domain.SkillSnapshot{}, packages...)
	invalid.Skills[0].InvocationPolicy = "unknown"
	if err := ValidateFrozen(invalid.Skills); err == nil {
		t.Fatal("corrupt frozen package policy accepted")
	}
	dependent := snapshot
	dependent.Skills = append([]domain.SkillSnapshot{}, packages...)
	dependent.Skills[0].RequiredTools = []string{"calculator"}
	dependent.Skills[0].Hash = packageHash(dependent.Skills[0])
	if _, err := service.Active(WithReadyTools(ctx, nil), &dependent); err == nil || !strings.Contains(err.Error(), "effective ready Tool") {
		t.Fatalf("effective dependency revocation ignored: %v", err)
	}
}
