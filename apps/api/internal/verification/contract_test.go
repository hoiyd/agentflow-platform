package verification

import (
	"testing"

	"agentflow-platform/apps/api/internal/domain"
)

func TestFreezeContractNormalizesAndHashesEffectiveDefinition(t *testing.T) {
	registry := NewRegistry(Options{})
	input := &domain.CompletionContract{
		ID: "contract_test",
		Verifiers: []domain.VerifierSpec{{
			ID: "schema", Type: domain.VerifierJSONSchema, Required: true,
			JSONSchema: &domain.JSONSchemaVerifierConfig{Schema: map[string]any{
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
	input.Verifiers[0].JSONSchema.Schema["type"] = "string"
	if frozen.Verifiers[0].JSONSchema.Schema["type"] != "object" {
		t.Fatal("frozen contract retained caller-owned schema map")
	}

	again, err := registry.FreezeContract(&domain.CompletionContract{
		ID: "contract_test",
		Verifiers: []domain.VerifierSpec{{
			ID: "schema", Type: domain.VerifierJSONSchema, Required: true,
			JSONSchema: &domain.JSONSchemaVerifierConfig{Schema: map[string]any{
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

func TestFreezeContractRejectsNonGatingOrUnsafeDefinitions(t *testing.T) {
	registry := NewRegistry(Options{})
	tests := []struct {
		name     string
		contract domain.CompletionContract
	}{
		{name: "no required verifier", contract: domain.CompletionContract{Verifiers: []domain.VerifierSpec{{
			ID: "schema", Type: domain.VerifierJSONSchema,
			JSONSchema: &domain.JSONSchemaVerifierConfig{Schema: map[string]any{"type": "object"}},
		}}}},
		{name: "http side effect", contract: domain.CompletionContract{Verifiers: []domain.VerifierSpec{{
			ID: "http", Type: domain.VerifierHTTP, Required: true,
			HTTP: &domain.HTTPVerifierConfig{Method: "POST", URL: "https://example.com", ExpectedStatus: 200},
		}}}},
		{name: "absolute working directory", contract: domain.CompletionContract{Verifiers: []domain.VerifierSpec{{
			ID: "command", Type: domain.VerifierCommand, Required: true,
			Command: &domain.CommandVerifierConfig{Args: []string{"go", "test"}, WorkingDirectory: "/tmp"},
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
