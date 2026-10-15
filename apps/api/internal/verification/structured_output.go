package verification

import (
	"encoding/json"
	"fmt"

	"agentflow-platform/apps/api/internal/domain"
)

// OutputSchema reads the single schema authority in a frozen Completion Contract.
func OutputSchema(contract *domain.CompletionContract) (map[string]any, error) {
	if contract == nil || contract.Output == nil {
		return nil, nil
	}
	for _, spec := range contract.Verifiers {
		if spec.ID == contract.Output.VerifierID && spec.Type == domain.VerifierJSONSchema && spec.Required {
			config, err := decodeConfig[JSONSchemaConfig](&spec)
			return config.Schema, err
		}
	}
	return nil, invalidContract("output.verifier_id must reference a required JSON Schema verifier")
}

func normalizeStructuredOutput(contract *domain.CompletionContract) error {
	if contract.Output == nil {
		return nil
	}
	if contract.Output.Mode != "strict" && contract.Output.Mode != "local" {
		return invalidContract("output.mode must be strict or local")
	}
	if contract.Policy.Mode != domain.VerificationAllMustPass {
		return invalidContract("structured output requires all_must_pass")
	}
	schema, err := OutputSchema(contract)
	if err != nil {
		return err
	}
	if schema["type"] != "object" {
		return invalidContract("structured output requires an object root")
	}
	if dialect, declared := schema["$schema"]; declared && dialect != "https://json-schema.org/draft/2020-12/schema" && dialect != "https://json-schema.org/draft/2020-12/schema#" {
		return invalidContract("structured output supports only JSON Schema 2020-12")
	}
	encoded, err := json.Marshal(schema)
	if err != nil || len(encoded) > 16*1024 {
		return invalidContract("structured output schema exceeds 16 KiB")
	}
	if contract.Output.Mode == "strict" {
		if err := strictOutputSchema(schema, 0); err != nil {
			return invalidContract(err.Error())
		}
	}
	contract.Output.Version = domain.StructuredOutputVersion
	return nil
}

// Deliberately small portable subset. No provider-specific keyword stripping:
// unsupported schemas must use explicit local validation instead.
func strictOutputSchema(schema map[string]any, depth int) error {
	if depth > 10 {
		return fmt.Errorf("strict output schema exceeds 10 levels")
	}
	for key := range schema {
		switch key {
		case "type", "properties", "required", "additionalProperties", "items", "enum", "description", "title":
		default:
			return fmt.Errorf("strict output schema keyword %q is unsupported; use local mode", key)
		}
	}
	t, ok := schema["type"].(string)
	if !ok {
		return fmt.Errorf("strict output schema requires a concrete type")
	}
	switch t {
	case "object":
		properties, ok := schema["properties"].(map[string]any)
		if !ok || schema["additionalProperties"] != false {
			return fmt.Errorf("strict objects require properties and additionalProperties=false")
		}
		required, _ := schema["required"].([]any)
		if len(required) != len(properties) {
			return fmt.Errorf("strict objects require every property")
		}
		for _, name := range required {
			if _, ok := properties[fmt.Sprint(name)]; !ok {
				return fmt.Errorf("strict required property is missing")
			}
		}
		for _, child := range properties {
			nested, ok := child.(map[string]any)
			if !ok {
				return fmt.Errorf("strict property must be a schema object")
			}
			if err := strictOutputSchema(nested, depth+1); err != nil {
				return err
			}
		}
	case "array":
		items, ok := schema["items"].(map[string]any)
		if !ok {
			return fmt.Errorf("strict arrays require an items schema")
		}
		return strictOutputSchema(items, depth+1)
	case "string", "number", "integer", "boolean", "null":
	default:
		return fmt.Errorf("strict output schema type is unsupported")
	}
	return nil
}
