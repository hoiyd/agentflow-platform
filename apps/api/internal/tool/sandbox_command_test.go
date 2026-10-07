package tool

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/sandbox"
	"agentflow-platform/apps/api/internal/tool/policy"
)

func TestSandboxBindingDefaultsAndSchema(t *testing.T) {
	binding := SandboxCommandTool(nil)
	catalog, err := BuildCatalog(DefaultConfig(), binding)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range catalog.List() {
		if item.Name == "sandbox_command" && (item.Enabled || item.UnavailableReason != "sandbox_disabled") {
			t.Fatalf("unsafe default: %+v", item)
		}
	}
	if binding.Descriptor.Security.SideEffect != policy.SideEffectInternalWrite || binding.Descriptor.JournalMode() != SideEffectInternal || binding.Descriptor.SideEffect.RetryWithSameKey {
		t.Fatal("sandbox writes must use non-retryable internal receipts")
	}
	if _, err := binding.Handler(t.Context(), json.RawMessage(`{"args":["/bin/sh"]}`)); err == nil {
		t.Fatal("disabled runner executed")
	}
}

func TestSandboxBindingPublishesConfiguredExecutables(t *testing.T) {
	runner, err := sandbox.New(sandbox.Options{Executable: "/usr/bin/true", StateDirectory: filepath.Join(t.TempDir(), "state"), AllowedCommands: []string{"/usr/bin/python3", "/bin/sh"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runner.Close(context.Background()) })
	commands := runner.AllowedCommands()
	commands[0] = "/not-allowed"
	binding := SandboxCommandTool(runner)
	data, err := json.Marshal(binding.Descriptor.Parameters)
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Properties map[string]struct {
			Description string `json:"description"`
			PrefixItems []struct {
				Enum []string `json:"enum"`
			} `json:"prefixItems"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}
	args := schema.Properties["args"]
	if len(args.PrefixItems) != 1 || !reflect.DeepEqual(args.PrefixItems[0].Enum, []string{"/bin/sh", "/usr/bin/python3"}) {
		t.Fatalf("executable whitelist is missing from the model contract: %s", data)
	}
	if !strings.Contains(args.Description, "/usr/bin/python3") || !strings.Contains(args.Description, "/bin/sh") {
		t.Fatalf("model instructions omit configured executable paths: %s", args.Description)
	}
}

// Real CLI fault injection checks the Binding's public error contract. A
// cleanup failure must dominate cancellation so operators see an uncertain VM.
func TestSandboxBindingFaultClassification(t *testing.T) {
	for _, scenario := range []struct {
		name string
		code ErrorCode
	}{
		{"invalid-json", ErrorInvalidArgs}, {"denied", ErrorSecurityPolicyDenied},
		{"canceled", ErrorExecutionCanceled}, {"timeout", ErrorExecutionTimeout},
		{"closed", ErrorExecutionUnavailable}, {"create-failed", ErrorExecutionUnavailable},
		{"cleanup-failed", ErrorEffectReconciliation}, {"canceled-cleanup-failed", ErrorEffectReconciliation},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			root := t.TempDir()
			cli := filepath.Join(root, "sbx-fixture")
			namePath := filepath.Join(root, "name")
			startedPath := filepath.Join(root, "started")
			script := "#!/bin/sh\ncase \"$1\" in\ncreate) printf '%s' \"$3\" > '" + namePath + "';;\nexec) printf receipt;;\nrm) exit 0;;\nls) exit 0;;\nesac\n"
			if scenario.name == "create-failed" {
				script = strings.ReplaceAll(script, "create) printf", "create) exit 2; printf")
			}
			if scenario.name == "timeout" || scenario.name == "canceled-cleanup-failed" {
				script = strings.ReplaceAll(script, "exec) printf receipt", "exec) printf started > '"+startedPath+"'; exec /bin/sleep 60")
			}
			if strings.Contains(scenario.name, "cleanup-failed") {
				script = strings.ReplaceAll(script, "rm) exit 0", "rm) exit 2")
				script = strings.ReplaceAll(script, "ls) exit 0", "ls) /bin/cat '"+namePath+"'")
			}
			if err := os.WriteFile(cli, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			runner, err := sandbox.New(sandbox.Options{Executable: cli, StateDirectory: filepath.Join(root, "state"), AllowedCommands: []string{"/bin/sh"}, Timeout: time.Second})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = runner.Close(context.Background()) })
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			args := json.RawMessage(`{"args":["/bin/sh"]}`)
			switch scenario.name {
			case "invalid-json":
				args = json.RawMessage(`{`)
			case "denied":
				args = json.RawMessage(`{"args":["/not-allowed"]}`)
			case "canceled":
				cancel()
			case "closed":
				if err := runner.Close(t.Context()); err != nil {
					t.Fatal(err)
				}
			case "canceled-cleanup-failed":
				// Wait for exec, not create's output: create may still be returning.
				finished := make(chan struct{})
				go func() {
					defer close(finished)
					for {
						if _, err := os.Stat(startedPath); err == nil {
							cancel()
							return
						}
						select {
						case <-ctx.Done():
							return
						case <-time.After(time.Millisecond):
						}
					}
				}()
				defer func() { cancel(); <-finished }()
			}
			_, err = SandboxCommandTool(runner).Handler(ctx, args)
			var failure *ExecutionError
			if !errors.As(err, &failure) || failure.Code != scenario.code {
				t.Fatalf("wrong classification: %v", err)
			}
			info := failure.FailureInfo()
			if info.Source != "tool" || info.Code != string(scenario.code) {
				t.Fatalf("misleading public metadata: %+v", info)
			}
			encoded, _ := json.Marshal(failure)
			if strings.Contains(string(encoded), cli) {
				t.Fatal("host CLI path leaked to model")
			}
		})
	}
}

func TestSandboxBindingCapacityDoesNotBecomeProviderOverload(t *testing.T) {
	root := t.TempDir()
	cli, started := filepath.Join(root, "sbx-fixture"), filepath.Join(root, "started")
	if err := os.WriteFile(cli, []byte("#!/bin/sh\nif [ \"$1\" = exec ]; then printf x > '"+started+"'; exec /bin/sleep 60; fi\n"), 0700); err != nil {
		t.Fatal(err)
	}
	runner, err := sandbox.New(sandbox.Options{Executable: cli, StateDirectory: filepath.Join(root, "state"), AllowedCommands: []string{"/bin/sh"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runner.Close(context.Background()) })
	binding := SandboxCommandTool(runner)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	finished := make(chan error, 1)
	go func() { _, err := binding.Handler(ctx, json.RawMessage(`{"args":["/bin/sh"]}`)); finished <- err }()
	defer func() { cancel(); <-finished }()
	for {
		if _, err := os.Stat(started); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("fixture did not start")
		case <-time.After(time.Millisecond):
		}
	}
	_, err = binding.Handler(t.Context(), json.RawMessage(`{"args":["/bin/sh"]}`))
	var failure *ExecutionError
	if !errors.As(err, &failure) || failure.Code != ErrorExecutionCapacity || !failure.FailureInfo().Retryable {
		t.Fatalf("capacity: %v", err)
	}
}

func TestSandboxProfileParticipatesInFrozenToolDefinition(t *testing.T) {
	var revisions []string
	for _, cpus := range []int{1, 1, 2} {
		runner, err := sandbox.New(sandbox.Options{Executable: "/usr/bin/true", StateDirectory: filepath.Join(t.TempDir(), "state"), AllowedCommands: []string{"/bin/sh"}, CPUs: cpus})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = runner.Close(context.Background()) })
		catalog, err := NewCatalog(SandboxCommandTool(runner))
		if err != nil {
			t.Fatal(err)
		}
		installed, ok := catalog.Installed("sandbox_command")
		if !ok || installed.Descriptor.DefinitionRevision == "" {
			t.Fatal("sandbox is missing a frozen identity")
		}
		data, _ := json.Marshal(installed.Descriptor)
		if strings.Contains(string(data), "/usr/bin/true") {
			t.Fatal("host executable leaked into frozen definition")
		}
		revisions = append(revisions, installed.Descriptor.DefinitionRevision)
	}
	if revisions[0] != revisions[1] || revisions[0] == revisions[2] {
		t.Fatal("host paths must not drift; changed sandbox profiles must change the Tool definition")
	}
}

func TestSandboxBindingUsesRealExecutorAndDurableReplay(t *testing.T) {
	directory := t.TempDir()
	executable := filepath.Join(directory, "sbx-fixture")
	marker := filepath.Join(directory, "executed")
	// This fixture exercises the real CLI boundary but does not prove VM isolation.
	script := "#!/bin/sh\ncase \"$1\" in\nexec) printf x >> '" + marker + "'; printf 'isolated output';;\n*) exit 0;;\nesac\n"
	if err := os.WriteFile(executable, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	runner, err := sandbox.New(sandbox.Options{Executable: executable, StateDirectory: filepath.Join(directory, "state"), AllowedCommands: []string{"/bin/sh"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runner.Close(context.Background()) })
	binding := SandboxCommandTool(runner)
	cfg := DefaultConfig()
	cfg.EnabledTools = append(cfg.EnabledTools, binding.Descriptor.Name)
	catalog, err := BuildCatalog(cfg, binding)
	if err != nil {
		t.Fatal(err)
	}
	request := ExecutionRequest{RunID: "run", TurnID: "turn", CallID: "call", Tool: "sandbox_command", Arguments: json.RawMessage(`{"args":["/bin/sh","-c","echo safe"]}`)}
	journal := &memoryEffectJournal{records: map[string]domain.ToolEffectRecord{}}
	executor := NewExecutor(catalog, ExecutorOptions{EffectJournal: journal})
	if result := executor.Execute(t.Context(), request); result.Error == nil || result.Error.Code != ErrorSecurityPolicyDenied {
		t.Fatalf("missing explicit grant was not denied: %+v", result)
	}
	cfg.SecurityPolicy.Rules = append(cfg.SecurityPolicy.Rules, policy.Rule{ID: "sandbox-execution", Tool: binding.Descriptor.Name, Action: policy.ActionAllow, Capability: binding.Descriptor.Security})
	catalog, err = BuildCatalog(cfg, binding)
	if err != nil {
		t.Fatal(err)
	}
	executor = NewExecutor(catalog, ExecutorOptions{EffectJournal: journal})
	for _, args := range []string{`{}`, `{"args":[]}`, `{"args":["/bin/sh"],"workspace":"/host"}`, `{"args":[1]}`, `{"args":["python","-c","print(45)"]}`, `{"args":["/not-allowed"]}`, `{"args":["/bin/bash"]}`} {
		bad := request
		bad.Arguments = json.RawMessage(args)
		if result := executor.Execute(t.Context(), bad); result.Error == nil || result.Error.Code != ErrorInvalidArgs {
			t.Fatalf("schema bypass: %+v", result)
		}
	}
	if len(journal.records) != 0 {
		t.Fatal("invalid arguments reserved a side-effect record before execution")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("invalid arguments reached the sandbox CLI")
	}
	for _, owner := range []string{"turn", "stage"} {
		request.CallID = owner
		if owner == "stage" {
			request.TurnID = ""
			request.StageID = "stage"
		}
		first := executor.Execute(t.Context(), request)
		if first.Error != nil {
			t.Fatal(first.Error)
		}
		data, _ := json.Marshal(first.Result)
		var receipt sandbox.Result
		if err := json.Unmarshal(data, &receipt); err != nil || receipt.Output != "isolated output" || !receipt.CleanupConfirmed {
			t.Fatalf("receipt: %s %v", data, err)
		}
		second := executor.Execute(t.Context(), request)
		if second.Error != nil || !second.Replayed {
			t.Fatalf("duplicate ran again: %+v", second)
		}
	}
	bytes, _ := os.ReadFile(marker)
	if string(bytes) != "xx" {
		t.Fatalf("expected one execution per owner, got %q", bytes)
	}
}
