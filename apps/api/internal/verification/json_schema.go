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

func (jsonSchemaVerifier) Type() domain.VerifierType { return domain.VerifierJSONSchema }
func (jsonSchemaVerifier) Version() string           { return "json-schema-2020-12-v1" }

func (jsonSchemaVerifier) Verify(ctx context.Context, spec domain.VerifierSpec, subject Subject) Result {
	if spec.JSONSchema == nil {
		return blocked("json schema config is missing")
	}
	if err := ctx.Err(); err != nil {
		return blocked("json schema verification was canceled")
	}
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	compiler.UseLoader(denyRemoteSchemaLoader{})
	const resource = "urn:agentflow:verification-schema"
	if err := compiler.AddResource(resource, spec.JSONSchema.Schema); err != nil {
		return blocked("load json schema: " + err.Error())
	}
	schema, err := compiler.Compile(resource)
	if err != nil {
		return blocked("compile json schema: " + err.Error())
	}
	decoder := json.NewDecoder(bytes.NewBufferString(subject.Value))
	decoder.UseNumber()
	var instance any
	if err := decoder.Decode(&instance); err != nil {
		return Result{Status: domain.VerificationFailed, Summary: "run output is not valid JSON: " + err.Error(), Output: subject.Value, OutputBytes: len(subject.Value)}
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return Result{Status: domain.VerificationFailed, Summary: "run output contains trailing JSON content", Output: subject.Value, OutputBytes: len(subject.Value)}
	}
	if err := schema.Validate(instance); err != nil {
		return Result{Status: domain.VerificationFailed, Summary: "json schema mismatch: " + singleLine(err.Error()), Output: subject.Value, OutputBytes: len(subject.Value)}
	}
	return Result{Status: domain.VerificationPassed, Summary: "run output matched JSON schema", Output: subject.Value, OutputBytes: len(subject.Value)}
}

type denyRemoteSchemaLoader struct{}

func (denyRemoteSchemaLoader) Load(location string) (any, error) {
	return nil, fmt.Errorf("remote schema reference is disabled: %s", location)
}

func singleLine(value string) string {
	return strings.Join(strings.Fields(value), " ")
}
