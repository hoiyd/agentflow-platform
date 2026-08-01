package verification

import (
	"context"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
)

func TestFreezeContractNormalizesAndHashesEffectiveDefinition(t *testing.T) {
	registry := NewRegistry(Options{})
	input := &domain.CompletionContract{
		ID: "contract_test",
		Verifiers: []domain.VerifierSpec{{
			ID: "schema", Type: domain.VerifierJSONSchema, Required: true,
			Config: map[string]any{"schema": map[string]any{
				"type": "object", "required": []any{"status"},
			}},
		}},
	}
	frozen, err := registry.FreezeContract(input)
	if err != nil {
		t.Fatalf("freeze contract: %v", err)
	}
	if frozen.Version != domain.CurrentCompletionContractVersion || frozen.SubjectType != "run_output" || frozen.Hash == "" {
		t.Fatalf("unexpected frozen contract: %#v", frozen)
	}
	if frozen.Verifiers[0].Version != "json-schema-2020-12-v1" || frozen.Verifiers[0].TimeoutMS != defaultVerifierTimeoutMS {
		t.Fatalf("verifier implementation was not frozen: %#v", frozen.Verifiers[0])
	}
	if frozen.Policy.Mode != domain.VerificationAllMustPass || frozen.Policy.MaxAttempts != 2 || frozen.Policy.OnExhausted != domain.VerificationWaitForUser {
		t.Fatalf("policy defaults were not frozen: %#v", frozen.Policy)
	}
	input.Verifiers[0].Config["schema"].(map[string]any)["type"] = "string"
	if frozen.Verifiers[0].Config["schema"].(map[string]any)["type"] != "object" {
		t.Fatal("frozen contract retained caller-owned schema map")
	}

	again, err := registry.FreezeContract(&domain.CompletionContract{
		ID: "contract_test",
		Verifiers: []domain.VerifierSpec{{
			ID: "schema", Type: domain.VerifierJSONSchema, Required: true,
			Config: map[string]any{"schema": map[string]any{
				"required": []any{"status"}, "type": "object",
			}},
		}},
	})
	if err != nil {
		t.Fatalf("freeze equivalent contract: %v", err)
	}
	if frozen.Hash != again.Hash {
		t.Fatalf("canonical contract hash changed: %s != %s", frozen.Hash, again.Hash)
	}
}

func TestFreezeContractAcceptsWebCompletionVerificationPolicy(t *testing.T) {
	registry := NewRegistry(Options{})
	contract, err := registry.FreezeContract(&domain.CompletionContract{
		SubjectType: "run_output",
		Verifiers: []domain.VerifierSpec{
			{
				ID: "response-length", Type: domain.VerifierTextConstraints, Required: true,
				Config: map[string]any{"min_characters": 120},
			},
			{
				ID: "source-policy", Type: domain.VerifierCitation, Required: true,
				Config: map[string]any{"min_citations": 2, "min_unique_hosts": 1, "require_https": true},
			},
		},
		Policy: domain.VerificationPolicy{
			Mode: domain.VerificationAllMustPass, MaxAttempts: 2, OnExhausted: domain.VerificationWaitForUser,
		},
	})
	if err != nil {
		t.Fatalf("freeze web verification policy: %v", err)
	}
	if contract.Version != domain.CurrentCompletionContractVersion || contract.Hash == "" || len(contract.Verifiers) != 2 {
		t.Fatalf("unexpected frozen web contract: %#v", contract)
	}
}

type customVerifier struct{}

func (customVerifier) Type() domain.VerifierType { return domain.VerifierType("custom_assertion") }
func (customVerifier) Version() string           { return "custom-v1" }
func (customVerifier) NormalizeConfig(spec *domain.VerifierSpec) error {
	if spec.Config == nil {
		spec.Config = map[string]any{}
	}
	return nil
}
func (customVerifier) Verify(context.Context, domain.VerifierSpec, Subject) Result {
	return Result{
		Status: domain.VerificationPassed, Summary: "custom assertion passed",
		Details: map[string]any{"score": 1.0},
		Artifacts: []Artifact{
			{Kind: "score", MediaType: "application/json", Content: `{"score":1}`, ByteSize: 11},
			{Kind: "notes", MediaType: "text/plain", Content: "passed", ByteSize: 6},
		},
	}
}

func TestRegistryAcceptsCustomVerifierWithoutCoreChanges(t *testing.T) {
	registry := NewRegistry(Options{})
	if err := registry.Register(customVerifier{}); err != nil {
		t.Fatalf("register custom verifier: %v", err)
	}
	frozen, err := registry.FreezeContract(&domain.CompletionContract{Verifiers: []domain.VerifierSpec{{
		ID: "custom", Type: domain.VerifierType("custom_assertion"), Required: true,
	}}})
	if err != nil {
		t.Fatalf("freeze custom verifier: %v", err)
	}
	if frozen.Verifiers[0].Version != "custom-v1" {
		t.Fatalf("custom implementation version was not frozen: %#v", frozen.Verifiers[0])
	}
	if err := registry.Register(customVerifier{}); err == nil {
		t.Fatal("duplicate verifier registration should fail")
	}
}

func TestFreezeContractRejectsNonGatingOrUnsafeDefinitions(t *testing.T) {
	registry := NewRegistry(Options{})
	tests := []struct {
		name     string
		contract domain.CompletionContract
	}{
		{name: "no required verifier", contract: domain.CompletionContract{Verifiers: []domain.VerifierSpec{{
			ID: "schema", Type: domain.VerifierJSONSchema,
			Config: map[string]any{"schema": map[string]any{"type": "object"}},
		}}}},
		{name: "http side effect", contract: domain.CompletionContract{Verifiers: []domain.VerifierSpec{{
			ID: "http", Type: domain.VerifierHTTP, Required: true,
			Config: map[string]any{"method": "POST", "url": "https://example.com", "expected_status": 200},
		}}}},
		{name: "http embedded credentials", contract: domain.CompletionContract{Verifiers: []domain.VerifierSpec{{
			ID: "http", Type: domain.VerifierHTTP, Required: true,
			Config: map[string]any{"method": "GET", "url": "https://user:secret@example.com", "expected_status": 200},
		}}}},
		{name: "absolute working directory", contract: domain.CompletionContract{Verifiers: []domain.VerifierSpec{{
			ID: "command", Type: domain.VerifierCommand, Required: true,
			Config: map[string]any{"args": []string{"go", "test"}, "working_directory": "/tmp"},
		}}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := registry.FreezeContract(&test.contract); !IsKind(err, ErrorInvalidContract) {
				t.Fatalf("expected invalid contract error, got %v", err)
			}
		})
	}
}

func TestSubjectAndSnapshotHashesBindExactContent(t *testing.T) {
	first := SubjectForRunOutput("result\n")
	second := SubjectForRunOutput("result")
	if first.Hash == second.Hash {
		t.Fatal("subject hash ignored a content change")
	}
	hash, err := SnapshotHash(&domain.RuntimeSnapshot{SchemaVersion: 3, Mode: "single"})
	if err != nil || hash == "" {
		t.Fatalf("snapshot hash: %q err=%v", hash, err)
	}
}
