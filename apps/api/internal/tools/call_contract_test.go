package tools

import (
	"context"
	"encoding/json"
	"testing"
)

func TestCatalogValidateCallDoesNotExecuteRuntimeSideEffects(t *testing.T) {
	handlerCalls := 0
	catalog, err := NewCatalog(Binding{
		Descriptor: Descriptor{
			Name: "lookup", Parameters: ObjectSchema(map[string]any{
				"query": map[string]any{"type": "string", "minLength": 1},
			}, []string{"query"}),
		},
		Handler: func(context.Context, json.RawMessage) (any, error) {
			handlerCalls++
			return nil, nil
		},
	})
	if err != nil {
		t.Fatalf("new catalog: %v", err)
	}
	call, executionErr := catalog.ValidateCall("lookup", json.RawMessage(`{"query":"status"}`))
	if executionErr != nil {
		t.Fatalf("validate call: %#v", executionErr)
	}
	if string(call.Arguments) != `{"query":"status"}` || call.ArgumentsHash == "" || call.DefinitionRevision == "" {
		t.Fatalf("validation did not return canonical contract identity: %#v", call)
	}
	if handlerCalls != 0 {
		t.Fatalf("validation crossed the handler boundary: calls=%d", handlerCalls)
	}
}

func TestCatalogValidateCallReturnsTypedContractFailures(t *testing.T) {
	catalog, err := NewCatalog(Binding{
		Descriptor: Descriptor{
			Name: "lookup", Parameters: ObjectSchema(map[string]any{
				"query": map[string]any{"type": "string"},
			}, []string{"query"}),
		},
		Handler: func(context.Context, json.RawMessage) (any, error) { return nil, nil },
	})
	if err != nil {
		t.Fatalf("new catalog: %v", err)
	}

	brokenCatalog, err := catalog.CloneWith()
	if err != nil {
		t.Fatalf("clone catalog: %v", err)
	}
	brokenBinding := brokenCatalog.bindings["lookup"]
	brokenBinding.contract = nil
	brokenCatalog.bindings["lookup"] = brokenBinding
	tests := []struct {
		name      string
		catalog   *Catalog
		tool      string
		arguments json.RawMessage
		wantCode  ErrorCode
	}{
		{name: "nil catalog", tool: "lookup", arguments: json.RawMessage(`{}`), wantCode: ErrorToolNotFound},
		{name: "missing Tool", catalog: catalog, tool: "missing", arguments: json.RawMessage(`{}`), wantCode: ErrorToolNotFound},
		{name: "invalid arguments", catalog: catalog, tool: "lookup", arguments: json.RawMessage(`{}`), wantCode: ErrorInvalidArgs},
		{name: "missing compiled contract", catalog: brokenCatalog, tool: "lookup", arguments: json.RawMessage(`{"query":"status"}`), wantCode: ErrorExecutionFailed},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, executionErr := test.catalog.ValidateCall(test.tool, test.arguments)
			if executionErr == nil || executionErr.Code != test.wantCode {
				t.Fatalf("error = %#v, want %s", executionErr, test.wantCode)
			}
		})
	}
}
