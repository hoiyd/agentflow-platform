package tool

import (
	"agentflow-platform/apps/api/internal/tool/policy"
	"fmt"
)

// Only Catalog-issued, unchanged definitions can reuse a compiled validator.
func registeredArgumentContract(binding *Binding) (*argumentContract, error) {
	// An explicit new definition is revalidated; it never reuses the old contract.
	if binding.contract == nil || binding.Descriptor.DefinitionRevision == "" {
		return compileArgumentContract(&binding.Descriptor)
	}
	revision, err := toolDefinitionRevision(binding.Descriptor)
	if err != nil {
		return nil, err
	}
	if binding.Descriptor.SchemaVersion != ToolSchemaVersion || binding.Descriptor.DefinitionRevision != binding.contract.definitionRevision || revision != binding.contract.definitionRevision {
		return nil, fmt.Errorf("compiled Binding definition changed")
	}
	binding.Descriptor.Parameters = copySchema(binding.Descriptor.Parameters)
	return binding.contract, nil
}

func copyBinding(binding Binding) Binding {
	binding.Descriptor.Parameters = copySchema(binding.Descriptor.Parameters)
	binding.Descriptor.Security.Scope = policy.NormalizeScope(binding.Descriptor.Security.Scope)
	return binding
}

// Registered schemas contain only normalized JSON maps, arrays and scalars.
func copySchema(schema map[string]any) map[string]any {
	if schema == nil {
		return nil
	}
	return copyJSONValue(schema).(map[string]any)
}

func copyJSONValue(value any) any {
	switch value := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(value))
		for key, item := range value {
			result[key] = copyJSONValue(item)
		}
		return result
	case []any:
		result := make([]any, len(value))
		for index, item := range value {
			result[index] = copyJSONValue(item)
		}
		return result
	default:
		return value
	}
}
