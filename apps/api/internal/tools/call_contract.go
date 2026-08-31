package tools

import (
	"encoding/json"
	"fmt"
)

// ValidatedCall is the canonical, side-effect-free result of applying the
// Catalog contract to one model-produced Tool call. It is safe to reuse in
// deterministic evaluation because it does not charge Budget or invoke a Binding.
type ValidatedCall struct {
	Tool               string
	Arguments          json.RawMessage
	DefinitionRevision string
	ArgumentsHash      string
}

// ValidateCall applies the same contract used by Executor without invoking the
// Binding. Offline evaluators use it to distinguish selection errors from
// execution failures.
func (c *Catalog) ValidateCall(name string, arguments json.RawMessage) (ValidatedCall, *ExecutionError) {
	_, call, executionErr := c.prepareCall(name, arguments)
	return call, executionErr
}

func (c *Catalog) prepareCall(name string, arguments json.RawMessage) (Binding, ValidatedCall, *ExecutionError) {
	call := ValidatedCall{Tool: name, Arguments: append(json.RawMessage(nil), normalizeArguments(arguments)...)}
	if c == nil {
		return Binding{}, call, executionError(ErrorToolNotFound, fmt.Sprintf("tool %q not found", name), nil)
	}
	binding, ok := c.Resolve(name)
	if !ok {
		return Binding{}, call, executionError(ErrorToolNotFound, fmt.Sprintf("tool %q not found", name), nil)
	}
	if binding.contract == nil {
		return binding, call, executionError(ErrorExecutionFailed, "tool argument contract is unavailable", nil)
	}
	canonical, issue := binding.contract.validate(call.Arguments)
	if canonical != nil {
		call.Arguments = canonical
		call.DefinitionRevision = binding.contract.definitionRevision
		call.ArgumentsHash = argumentsHash(call.DefinitionRevision, canonical)
	}
	if issue != nil {
		return binding, call, invalidArgumentsError(issue)
	}
	return binding, call, nil
}
