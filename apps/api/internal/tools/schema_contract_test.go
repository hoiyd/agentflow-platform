package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
)

func TestCatalogNormalizesAndVersionsToolSchema(t *testing.T) {
	first, err := NewCatalog(Binding{
		Descriptor: Descriptor{
			Name: "lookup", Description: "Lookup a record.",
			Parameters: map[string]any{
				"required": []string{"id"}, "type": "object",
				"properties": map[string]any{"id": map[string]any{"type": "string"}},
			},
		},
		Handler: successfulContractHandler,
	})
	if err != nil {
		t.Fatalf("new catalog: %v", err)
	}
	installed, ok := first.Installed("lookup")
	if !ok {
		t.Fatal("normalized binding was not installed")
	}
	if installed.Descriptor.SchemaVersion != ToolSchemaVersion || installed.Descriptor.DefinitionRevision == "" {
		t.Fatalf("schema identity was not derived: %#v", installed.Descriptor)
	}
	if installed.Descriptor.Parameters["additionalProperties"] != false {
		t.Fatalf("root additional properties did not default to false: %#v", installed.Descriptor.Parameters)
	}
	definition := first.Definitions()[0]["function"].(map[string]any)
	encodedModelSchema, _ := json.Marshal(definition["parameters"])
	encodedContractSchema, _ := json.Marshal(installed.Descriptor.Parameters)
	if string(encodedModelSchema) != string(encodedContractSchema) {
		t.Fatalf("model and validator schemas differ: model=%s contract=%s", encodedModelSchema, encodedContractSchema)
	}

	second, err := NewCatalog(Binding{
		Descriptor: Descriptor{
			Name: "lookup", Description: "Lookup a record.",
			Parameters: map[string]any{
				"properties": map[string]any{"id": map[string]any{"type": "string"}},
				"type":       "object", "required": []string{"id"},
			},
		},
		Handler: successfulContractHandler,
	})
	if err != nil {
		t.Fatalf("new reordered catalog: %v", err)
	}
	reordered, _ := second.Installed("lookup")
	if reordered.Descriptor.DefinitionRevision != installed.Descriptor.DefinitionRevision {
		t.Fatalf("map order changed revision: %s != %s", reordered.Descriptor.DefinitionRevision, installed.Descriptor.DefinitionRevision)
	}
}

func TestCatalogRejectsInvalidToolSchemaContracts(t *testing.T) {
	tests := []struct {
		name       string
		descriptor Descriptor
		want       string
	}{
		{
			name: "non JSON schema value",
			descriptor: Descriptor{Name: "tool", Parameters: map[string]any{
				"type": "object", "invalid": make(chan struct{}),
			}},
			want: "JSON-compatible",
		},
		{
			name:       "unsupported contract version",
			descriptor: Descriptor{Name: "tool", SchemaVersion: "draft-07-v1", Parameters: ObjectSchema(nil, nil)},
			want:       "unsupported schema version",
		},
		{
			name: "unsupported declared draft",
			descriptor: Descriptor{Name: "tool", Parameters: map[string]any{
				"$schema": "http://json-schema.org/draft-07/schema#", "type": "object",
			}},
			want: "unsupported JSON Schema draft",
		},
		{
			name:       "non object root",
			descriptor: Descriptor{Name: "tool", Parameters: map[string]any{"type": "array"}},
			want:       "root schema type must be object",
		},
		{
			name:       "invalid required keyword",
			descriptor: Descriptor{Name: "tool", Parameters: map[string]any{"type": "object", "required": "id"}},
			want:       "compile schema",
		},
		{
			name: "remote reference",
			descriptor: Descriptor{Name: "tool", Parameters: map[string]any{
				"type": "object", "properties": map[string]any{"item": map[string]any{"$ref": "https://example.com/schema.json"}},
			}},
			want: "remote schema reference is disabled",
		},
		{
			name: "oversized schema",
			descriptor: Descriptor{Name: "tool", Parameters: map[string]any{
				"type": "object", "description": strings.Repeat("x", MaximumToolSchemaBytes),
			}},
			want: "schema exceeds",
		},
		{
			name:       "stale declared revision",
			descriptor: Descriptor{Name: "tool", DefinitionRevision: "sha256:stale", Parameters: ObjectSchema(nil, nil)},
			want:       "declared definition revision",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewCatalog(Binding{Descriptor: test.descriptor, Handler: successfulContractHandler})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected %q, got %v", test.want, err)
			}
		})
	}
}

func TestSchemaContractAcceptsSupportedDraftAndMatchingRevision(t *testing.T) {
	descriptor := Descriptor{Name: "tool", Parameters: map[string]any{
		"$schema": "https://json-schema.org/draft/2020-12/schema#", "type": "object",
	}}
	first, err := NewCatalog(Binding{Descriptor: descriptor, Handler: successfulContractHandler})
	if err != nil {
		t.Fatalf("register supported draft: %v", err)
	}
	installed, _ := first.Installed("tool")
	descriptor.DefinitionRevision = installed.Descriptor.DefinitionRevision
	if _, err := NewCatalog(Binding{Descriptor: descriptor, Handler: successfulContractHandler}); err != nil {
		t.Fatalf("register matching revision: %v", err)
	}
	if _, err := compileArgumentContract(nil); err == nil || !strings.Contains(err.Error(), "parameters are required") {
		t.Fatalf("expected nil contract error, got %v", err)
	}
	if _, err := toolDefinitionRevision(Descriptor{Parameters: map[string]any{"invalid": make(chan struct{})}}); err == nil {
		t.Fatal("expected non-JSON definition revision error")
	}
	if _, err := LegacyDefinitionRevision(Descriptor{Parameters: map[string]any{"invalid": make(chan struct{})}}); err == nil {
		t.Fatal("expected non-JSON legacy definition revision error")
	}
}

func TestExecutorRejectsSchemaViolationsBeforeHandler(t *testing.T) {
	var calls atomic.Int32
	catalog, err := NewCatalog(Binding{
		Descriptor: Descriptor{Name: "profile", Parameters: ObjectSchema(map[string]any{
			"profile": ObjectSchema(map[string]any{
				"age":  map[string]any{"type": "integer", "minimum": 0},
				"role": map[string]any{"type": "string", "enum": []string{"reader", "writer"}},
			}, []string{"age", "role"}),
		}, []string{"profile"})},
		Handler: func(context.Context, json.RawMessage) (any, error) {
			calls.Add(1)
			return map[string]any{"ok": true}, nil
		},
	})
	if err != nil {
		t.Fatalf("new catalog: %v", err)
	}
	executor := NewExecutor(catalog, ExecutorOptions{})
	tests := []struct {
		name string
		args string
		code string
		path string
	}{
		{name: "missing required", args: `{}`, code: "required", path: "/profile"},
		{name: "nested type", args: `{"profile":{"age":"secret-value","role":"reader"}}`, code: "type", path: "/profile/age"},
		{name: "unknown nested field", args: `{"profile":{"age":1,"role":"reader","password":"secret-value"}}`, code: "additional_properties", path: "/profile/password"},
		{name: "enum", args: `{"profile":{"age":1,"role":"secret-value"}}`, code: "enum", path: "/profile/role"},
		{name: "minimum", args: `{"profile":{"age":-1,"role":"reader"}}`, code: "minimum", path: "/profile/age"},
		{name: "root type", args: `[]`, code: "type", path: ""},
		{name: "malformed", args: `{"profile":`, code: "invalid_json", path: ""},
		{name: "trailing", args: `{} {}`, code: "invalid_json", path: ""},
		{name: "oversized", args: `{"value":"` + strings.Repeat("x", MaximumToolArgumentBytes) + `"}`, code: "arguments_too_large", path: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := executor.Execute(context.Background(), ExecutionRequest{Tool: "profile", Arguments: json.RawMessage(test.args)})
			if result.Error == nil || result.Error.Code != ErrorInvalidArgs || result.Error.Argument == nil {
				t.Fatalf("expected typed argument error, got %#v", result.Error)
			}
			if result.Error.Argument.Code != test.code || result.Error.Argument.Path != test.path {
				t.Fatalf("unexpected issue: %#v", result.Error.Argument)
			}
			encoded, _ := json.Marshal(result.Error)
			if strings.Contains(string(encoded), "secret-value") {
				t.Fatalf("argument error leaked submitted value: %s", encoded)
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatalf("invalid arguments reached handler %d times", calls.Load())
	}
}

func TestValidationIssueFallbackAndSafeMessages(t *testing.T) {
	issue := validationIssue(errors.New("validator unavailable"))
	if issue.Code != "schema_validation" || issue.Message == "" {
		t.Fatalf("unexpected fallback issue: %#v", issue)
	}
	for _, code := range []string{
		"required", "additional_properties", "type", "enum", "min_length", "max_length",
		"minimum", "maximum", "min_items", "max_items", "pattern", "unknown",
	} {
		if message := safeValidationMessage(code); message == "" || strings.Contains(message, "secret") {
			t.Fatalf("unsafe message for %s: %q", code, message)
		}
	}
}

func TestExecutorCanonicalizesArgumentsForTracingAndEffects(t *testing.T) {
	tracer := &recordingTracer{}
	executor := NewExecutor(DefaultCatalog(), ExecutorOptions{Tracer: tracer})
	first := executor.Execute(context.Background(), ExecutionRequest{
		CallID: "first", Tool: "calculator", Arguments: json.RawMessage(`{ "expression" : "1 + 1" }`),
	})
	firstStarted := tracer.started
	second := executor.Execute(context.Background(), ExecutionRequest{
		CallID: "second", Tool: "calculator", Arguments: json.RawMessage(`{"expression":"1 + 1"}`),
	})
	if first.Error != nil || second.Error != nil {
		t.Fatalf("canonical executions failed: first=%#v second=%#v", first.Error, second.Error)
	}
	if string(first.Arguments) != `{"expression":"1 + 1"}` || string(first.Arguments) != string(second.Arguments) {
		t.Fatalf("arguments were not canonicalized: %s / %s", first.Arguments, second.Arguments)
	}
	if first.ArgumentsHash == "" || first.ArgumentsHash != second.ArgumentsHash {
		t.Fatalf("equivalent arguments have different hashes: %q / %q", first.ArgumentsHash, second.ArgumentsHash)
	}
	if first.DefinitionRevision == "" || first.DefinitionRevision != second.DefinitionRevision {
		t.Fatalf("definition revision was not propagated: %#v / %#v", first, second)
	}
	if firstStarted.ArgumentsHash != first.ArgumentsHash || firstStarted.DefinitionRevision != first.DefinitionRevision {
		t.Fatalf("tracer did not receive canonical identity: %#v", firstStarted)
	}
	encoded, _ := json.Marshal(first)
	if strings.Contains(string(encoded), "arguments_hash") || strings.Contains(string(encoded), "definition_revision") {
		t.Fatalf("internal contract identity leaked into model-visible result: %s", encoded)
	}
	if sideEffectRequestHash(ExecutionRequest{Tool: "calculator", ArgumentsHash: first.ArgumentsHash}) != first.ArgumentsHash {
		t.Fatal("effect journal did not reuse canonical arguments hash")
	}
}

func TestBuiltInBindingsShareSchemaContract(t *testing.T) {
	tests := []struct {
		binding Binding
		args    string
	}{
		{binding: CalculatorTool(), args: `{"expression":"2 + 2"}`},
		{binding: CurrentTimeTool(), args: `{"timezone":"UTC"}`},
	}
	for _, test := range tests {
		catalog, err := NewCatalog(test.binding)
		if err != nil {
			t.Fatalf("register %s: %v", test.binding.Descriptor.Name, err)
		}
		result := NewExecutor(catalog, ExecutorOptions{}).Execute(context.Background(), ExecutionRequest{
			Tool: test.binding.Descriptor.Name, Arguments: json.RawMessage(test.args),
		})
		if result.Error != nil {
			t.Fatalf("execute %s: %v", test.binding.Descriptor.Name, result.Error)
		}
	}
}

func successfulContractHandler(context.Context, json.RawMessage) (any, error) {
	return map[string]any{"ok": true}, nil
}
