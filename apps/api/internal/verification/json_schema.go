package verification

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"agentflow-platform/apps/api/internal/domain"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

type jsonSchemaVerifier struct{}

type JSONSchemaConfig struct {
	Schema map[string]any `json:"schema"`
}

func (jsonSchemaVerifier) Type() domain.VerifierType { return domain.VerifierJSONSchema }
func (jsonSchemaVerifier) Version() string           { return "json-schema-2020-12-v1" }

func (jsonSchemaVerifier) NormalizeConfig(spec *domain.VerifierSpec) error {
	config, err := decodeConfig[JSONSchemaConfig](spec)
	if err != nil {
		return err
	}
	if len(config.Schema) == 0 {
		return invalidContract("json_schema verifier " + spec.ID + " requires schema")
	}
	if _, err := compileJSONSchema(config.Schema); err != nil {
		return invalidContract("json_schema verifier " + spec.ID + " has invalid schema: " + singleLine(err.Error()))
	}
	return freezeConfig(spec, config)
}

func (jsonSchemaVerifier) Verify(ctx context.Context, spec domain.VerifierSpec, subject Subject) Result {
	config, err := decodeConfig[JSONSchemaConfig](&spec)
	if err != nil {
		return blocked("json schema config is missing")
	}
	if err := ctx.Err(); err != nil {
		return blocked("json schema verification was canceled")
	}
	schema, err := compileJSONSchema(config.Schema)
	if err != nil {
		return blocked("compile json schema: " + err.Error())
	}
	decoder := json.NewDecoder(bytes.NewBufferString(subject.Value))
	decoder.UseNumber()
	var instance any
	if err := decoder.Decode(&instance); err != nil {
		return jsonSchemaResult(domain.VerificationFailed, "run output is not valid JSON: "+err.Error(), subject.Value)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return jsonSchemaResult(domain.VerificationFailed, "run output contains trailing JSON content", subject.Value)
	}
	if err := schema.Validate(instance); err != nil {
		return jsonSchemaResult(domain.VerificationFailed, "json schema mismatch: "+singleLine(err.Error()), subject.Value)
	}
	return jsonSchemaResult(domain.VerificationPassed, "run output matched JSON schema", subject.Value)
}

func compileJSONSchema(schemaDefinition map[string]any) (*jsonschema.Schema, error) {
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	compiler.UseLoader(denyRemoteSchemaLoader{})
	const resource = "urn:agentflow:verification-schema"
	if err := compiler.AddResource(resource, schemaDefinition); err != nil {
		return nil, err
	}
	return compiler.Compile(resource)
}

func jsonSchemaResult(status domain.VerificationStatus, summary, content string) Result {
	return Result{
		Status: status, Summary: summary,
		Artifacts: []Artifact{{
			Kind: "subject", MediaType: "application/json",
			Content: content, ByteSize: len(content),
		}},
	}
}

type denyRemoteSchemaLoader struct{}

func (denyRemoteSchemaLoader) Load(location string) (any, error) {
	return nil, fmt.Errorf("remote schema reference is disabled: %s", location)
}

func singleLine(value string) string {
	return strings.Join(strings.Fields(value), " ")
}
