package tools

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
)

const (
	ToolSchemaVersion        = "json-schema-2020-12-v1"
	MaximumToolSchemaBytes   = 64 * 1024
	MaximumToolArgumentBytes = 64 * 1024
)

type ArgumentValidationIssue struct {
	Code    string `json:"code"`
	Path    string `json:"path"`
	Message string `json:"message"`
}

type argumentContract struct {
	validator          *jsonschema.Schema
	definitionRevision string
}

func compileArgumentContract(descriptor *Descriptor) (*argumentContract, error) {
	if descriptor == nil || descriptor.Parameters == nil {
		return nil, fmt.Errorf("parameters are required")
	}
	if descriptor.SchemaVersion != "" && descriptor.SchemaVersion != ToolSchemaVersion {
		return nil, fmt.Errorf("unsupported schema version %q", descriptor.SchemaVersion)
	}

	normalized, encoded, err := normalizeToolSchema(descriptor.Parameters)
	if err != nil {
		return nil, err
	}
	if len(encoded) > MaximumToolSchemaBytes {
		return nil, fmt.Errorf("schema exceeds %d bytes", MaximumToolSchemaBytes)
	}
	if schemaType, ok := normalized["type"].(string); !ok || schemaType != "object" {
		return nil, fmt.Errorf("root schema type must be object")
	}
	if draft, ok := normalized["$schema"].(string); ok && !supportedToolSchemaDraft(draft) {
		return nil, fmt.Errorf("unsupported JSON Schema draft %q", draft)
	}
	if _, ok := normalized["additionalProperties"]; !ok {
		normalized["additionalProperties"] = false
		encoded, err = json.Marshal(normalized)
		if err != nil {
			return nil, fmt.Errorf("encode normalized schema: %w", err)
		}
		if len(encoded) > MaximumToolSchemaBytes {
			return nil, fmt.Errorf("schema exceeds %d bytes", MaximumToolSchemaBytes)
		}
	}

	validator, err := compileToolSchema(normalized)
	if err != nil {
		return nil, fmt.Errorf("compile schema: %w", err)
	}
	descriptor.Parameters = normalized
	descriptor.SchemaVersion = ToolSchemaVersion
	revision, err := toolDefinitionRevision(*descriptor)
	if err != nil {
		return nil, err
	}
	if descriptor.DefinitionRevision != "" && descriptor.DefinitionRevision != revision {
		return nil, fmt.Errorf("declared definition revision does not match normalized descriptor")
	}
	descriptor.DefinitionRevision = revision
	return &argumentContract{validator: validator, definitionRevision: revision}, nil
}

func normalizeToolSchema(parameters map[string]any) (map[string]any, []byte, error) {
	encoded, err := json.Marshal(parameters)
	if err != nil {
		return nil, nil, fmt.Errorf("parameters must be JSON-compatible: %w", err)
	}
	if len(encoded) > MaximumToolSchemaBytes {
		return nil, nil, fmt.Errorf("schema exceeds %d bytes", MaximumToolSchemaBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var normalized map[string]any
	if err := decoder.Decode(&normalized); err != nil || normalized == nil {
		return nil, nil, fmt.Errorf("parameters must be a JSON object")
	}
	canonical, err := json.Marshal(normalized)
	if err != nil {
		return nil, nil, fmt.Errorf("encode normalized schema: %w", err)
	}
	return normalized, canonical, nil
}

func compileToolSchema(definition map[string]any) (*jsonschema.Schema, error) {
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	compiler.UseLoader(denyRemoteToolSchemaLoader{})
	const resource = "urn:agentflow:tool-arguments"
	if err := compiler.AddResource(resource, definition); err != nil {
		return nil, err
	}
	return compiler.Compile(resource)
}

func supportedToolSchemaDraft(value string) bool {
	value = strings.TrimSuffix(strings.TrimSpace(value), "#")
	return value == "https://json-schema.org/draft/2020-12/schema"
}

type denyRemoteToolSchemaLoader struct{}

func (denyRemoteToolSchemaLoader) Load(location string) (any, error) {
	return nil, fmt.Errorf("remote schema reference is disabled: %s", location)
}

func toolDefinitionRevision(descriptor Descriptor) (string, error) {
	definition := struct {
		SchemaVersion string            `json:"schema_version"`
		Name          string            `json:"name"`
		Description   string            `json:"description"`
		Parameters    map[string]any    `json:"parameters"`
		Concurrency   ConcurrencyPolicy `json:"concurrency"`
		SideEffect    SideEffectPolicy  `json:"side_effect"`
		Security      any               `json:"security"`
	}{
		SchemaVersion: descriptor.SchemaVersion,
		Name:          descriptor.Name, Description: descriptor.Description,
		Parameters: descriptor.Parameters, Concurrency: descriptor.Concurrency,
		SideEffect: descriptor.SideEffect,
		Security:   descriptor.Security,
	}
	encoded, err := json.Marshal(definition)
	if err != nil {
		return "", fmt.Errorf("encode tool definition: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// LegacyDefinitionRevision reconstructs the version 10 digest, before Tool
// security capability became part of the frozen definition.
func LegacyDefinitionRevision(descriptor Descriptor) (string, error) {
	definition := struct {
		SchemaVersion string            `json:"schema_version"`
		Name          string            `json:"name"`
		Description   string            `json:"description"`
		Parameters    map[string]any    `json:"parameters"`
		Concurrency   ConcurrencyPolicy `json:"concurrency"`
		SideEffect    SideEffectPolicy  `json:"side_effect"`
	}{
		SchemaVersion: descriptor.SchemaVersion, Name: descriptor.Name,
		Description: descriptor.Description, Parameters: descriptor.Parameters,
		Concurrency: descriptor.Concurrency, SideEffect: descriptor.SideEffect,
	}
	encoded, err := json.Marshal(definition)
	if err != nil {
		return "", fmt.Errorf("encode legacy Tool definition: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func (c *argumentContract) validate(arguments json.RawMessage) (json.RawMessage, *ArgumentValidationIssue) {
	trimmed := bytes.TrimSpace(arguments)
	if len(trimmed) == 0 {
		trimmed = []byte(`{}`)
	}
	if len(trimmed) > MaximumToolArgumentBytes {
		return nil, &ArgumentValidationIssue{
			Code: "arguments_too_large", Message: fmt.Sprintf("tool arguments exceed %d bytes", MaximumToolArgumentBytes),
		}
	}

	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, &ArgumentValidationIssue{Code: "invalid_json", Message: "tool arguments are not valid JSON"}
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, &ArgumentValidationIssue{Code: "invalid_json", Message: "tool arguments contain trailing JSON content"}
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return nil, &ArgumentValidationIssue{Code: "invalid_json", Message: "tool arguments cannot be normalized"}
	}
	if err := c.validator.Validate(value); err != nil {
		return canonical, validationIssue(err)
	}
	return canonical, nil
}

func validationIssue(err error) *ArgumentValidationIssue {
	validationErr, ok := err.(*jsonschema.ValidationError)
	if !ok {
		return &ArgumentValidationIssue{Code: "schema_validation", Message: "tool arguments do not match the schema"}
	}
	leaf := firstValidationLeaf(validationErr)
	keyword := leaf.ErrorKind.KeywordPath()
	code := "schema_validation"
	if len(keyword) > 0 {
		code = snakeCase(keyword[len(keyword)-1])
	}
	path := jsonPointer(leaf.InstanceLocation)
	switch detail := leaf.ErrorKind.(type) {
	case *kind.Required:
		if len(detail.Missing) > 0 {
			missing := append([]string(nil), detail.Missing...)
			sort.Strings(missing)
			path = appendJSONPointer(path, missing[0])
		}
	case *kind.AdditionalProperties:
		if len(detail.Properties) > 0 {
			properties := append([]string(nil), detail.Properties...)
			sort.Strings(properties)
			path = appendJSONPointer(path, properties[0])
		}
	}
	return &ArgumentValidationIssue{Code: code, Path: path, Message: safeValidationMessage(code)}
}

func firstValidationLeaf(err *jsonschema.ValidationError) *jsonschema.ValidationError {
	for len(err.Causes) > 0 {
		err = err.Causes[0]
	}
	return err
}

func safeValidationMessage(code string) string {
	switch code {
	case "required":
		return "required property is missing"
	case "additional_properties":
		return "unknown property is not allowed"
	case "type":
		return "value has an invalid type"
	case "enum":
		return "value is not one of the allowed options"
	case "min_length", "max_length", "minimum", "maximum", "min_items", "max_items", "pattern":
		return "value is outside the allowed schema constraint"
	default:
		return "tool arguments do not match the schema"
	}
}

func jsonPointer(parts []string) string {
	path := ""
	for _, part := range parts {
		path = appendJSONPointer(path, part)
	}
	return path
}

func appendJSONPointer(path, part string) string {
	part = strings.ReplaceAll(strings.ReplaceAll(part, "~", "~0"), "/", "~1")
	return path + "/" + part
}

func snakeCase(value string) string {
	var output strings.Builder
	for index, r := range value {
		if unicode.IsUpper(r) && index > 0 {
			output.WriteByte('_')
		}
		output.WriteRune(unicode.ToLower(r))
	}
	return output.String()
}

func argumentsHash(definitionRevision string, arguments json.RawMessage) string {
	sum := sha256.Sum256([]byte(definitionRevision + "\x00" + string(arguments)))
	return "sha256:" + hex.EncodeToString(sum[:])
}
