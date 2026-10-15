package verification

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
)

func TestStructuredResultUsesOnlyLatestIntactVerifiedArtifact(t *testing.T) {
	contract, err := NewRegistry(Options{}).FreezeContract(&domain.CompletionContract{Output: &domain.StructuredOutputConfig{Mode: "local", VerifierID: "report"}, Verifiers: []domain.VerifierSpec{{ID: "report", Type: domain.VerifierJSONSchema, Required: true, Config: map[string]any{"schema": map[string]any{"type": "object"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	run := domain.Run{ID: "run", Status: domain.RunCompleted, VerificationStatus: domain.VerificationPassed, CompletionContract: contract}
	body := `{"answer":9007199254740993}`
	evidence := domain.VerificationEvidence{ID: "e", RunID: "run", ContractID: contract.ID, ContractVersion: contract.Version, VerifierID: "report", VerifierType: domain.VerifierJSONSchema, VerifierVersion: contract.Verifiers[0].Version, Status: domain.VerificationPassed, SubjectHash: "subject", Attempt: 1, ArtifactIDs: []string{"a"}}
	artifact := domain.VerificationArtifact{ID: "a", RunID: "run", EvidenceID: "e", Kind: "subject", Content: body, ContentHash: hashBytes([]byte(body)), MediaType: "application/json"}
	for _, tc := range []string{"valid", "running", "failed gate", "stale", "latest failed", "truncated", "redacted", "missing artifact", "other run", "changed contract", "wrong verifier revision"} {
		t.Run(tc, func(t *testing.T) {
			r, e, a := run, evidence, artifact
			records := []domain.VerificationEvidence{e}
			artifacts := []domain.VerificationArtifact{a}
			switch tc {
			case "changed contract":
				copy := *contract
				copy.Hash = "changed"
				r.CompletionContract = &copy
			case "wrong verifier revision":
				records[0].VerifierVersion = "other-version"
			case "running":
				r.Status = domain.RunRunning
			case "failed gate":
				r.VerificationStatus = domain.VerificationFailed
			case "stale":
				e.Status = domain.VerificationStale
				records = []domain.VerificationEvidence{e}
			case "latest failed":
				e.Attempt = 2
				e.Status = domain.VerificationFailed
				records = append(records, e)
			case "truncated":
				artifacts[0].Truncated = true
			case "redacted":
				artifacts[0].Content = `{"answer":0}`
			case "missing artifact":
				artifacts = nil
			case "other run":
				artifacts[0].RunID = "other"
			}
			result := StructuredResult(r, records, artifacts)
			if tc == "valid" {
				if string(result.Value) != body || result.EvidenceID != "e" {
					t.Fatalf("wrong result: %+v", result)
				}
			} else if len(result.Value) > 0 || result.UnavailableReason == "" {
				t.Fatalf("unverified result exposed: %+v", result)
			}
		})
	}
}

func TestStructuredCandidateFailuresAreTyped(t *testing.T) {
	for _, tc := range []struct{ body, code string }{{`{`, "invalid_json"}, {`{} {}`, "invalid_json"}, {`{"x":1}`, "schema_mismatch"}} {
		spec := domain.VerifierSpec{Config: map[string]any{"schema": map[string]any{"type": "object", "required": []any{"summary"}}}}
		result := (jsonSchemaVerifier{}).Verify(context.Background(), spec, SubjectForRunOutput(tc.body))
		if result.Status != domain.VerificationFailed || result.Details["reason_code"] != tc.code {
			t.Fatalf("wrong failure: %+v", result)
		}
	}
}

func TestVerificationOnlySchemaRetainsLargeCandidateBehavior(t *testing.T) {
	result := (jsonSchemaVerifier{}).Verify(context.Background(), domain.VerifierSpec{Config: map[string]any{"schema": map[string]any{"type": "object"}}}, SubjectForRunOutput(`{"text":"`+strings.Repeat("x", domain.MaxStructuredOutputBytes+1)+`"}`))
	if result.Status != domain.VerificationPassed {
		t.Fatalf("structured-result limits leaked into legacy verification: %+v", result.Status)
	}
}

// Failure inventory: unknown mode/reference, optional Schema, OR gates, remote
// refs, non-object roots and unsupported strict keywords must reject at freeze.
func TestStructuredOutputContractFreeze(t *testing.T) {
	for _, tc := range []struct {
		name, output, schema, policy string
		invalid                      bool
	}{
		{"strict", `{"mode":"strict","verifier_id":"report"}`, reportSchema, "all_must_pass", false},
		{"local", `{"mode":"local","verifier_id":"report"}`, reportSchema, "all_must_pass", false},
		{"unknown mode", `{"mode":"magic","verifier_id":"report"}`, reportSchema, "all_must_pass", true},
		{"unknown verifier", `{"mode":"local","verifier_id":"missing"}`, reportSchema, "all_must_pass", true},
		{"OR gate", `{"mode":"local","verifier_id":"report"}`, reportSchema, "any_may_pass", true},
		{"remote ref", `{"mode":"local","verifier_id":"report"}`, `{"$ref":"https://example.com/schema"}`, "all_must_pass", true},
		{"unsupported dialect", `{"mode":"local","verifier_id":"report"}`, `{"$schema":"http://json-schema.org/draft-07/schema#","type":"object"}`, "all_must_pass", true},
		{"strict format", `{"mode":"strict","verifier_id":"report"}`, `{"type":"object","properties":{"url":{"type":"string","format":"uri"}},"required":["url"],"additionalProperties":false}`, "all_must_pass", true},
		{"strict open object", `{"mode":"strict","verifier_id":"report"}`, `{"type":"object","additionalProperties":true}`, "all_must_pass", true},
		{"strict optional field", `{"mode":"strict","verifier_id":"report"}`, `{"type":"object","properties":{"x":{"type":"string"}},"required":[],"additionalProperties":false}`, "all_must_pass", true},
		{"strict unknown required field", `{"mode":"strict","verifier_id":"report"}`, `{"type":"object","properties":{"x":{"type":"string"}},"required":["y"],"additionalProperties":false}`, "all_must_pass", true},
		{"strict boolean schema", `{"mode":"strict","verifier_id":"report"}`, `{"type":"object","properties":{"x":true},"required":["x"],"additionalProperties":false}`, "all_must_pass", true},
		{"strict nullable union", `{"mode":"strict","verifier_id":"report"}`, `{"type":"object","properties":{"x":{"type":["string","null"]}},"required":["x"],"additionalProperties":false}`, "all_must_pass", true},
		{"strict array without item schema", `{"mode":"strict","verifier_id":"report"}`, `{"type":"object","properties":{"x":{"type":"array","items":true}},"required":["x"],"additionalProperties":false}`, "all_must_pass", true},
		{"strict depth", `{"mode":"strict","verifier_id":"report"}`, `{"type":"object","properties":{"x":` + strings.Repeat(`{"type":"array","items":`, 11) + `{"type":"string"}` + strings.Repeat(`}`, 11) + `},"required":["x"],"additionalProperties":false}`, "all_must_pass", true},
		{"schema bytes", `{"mode":"local","verifier_id":"report"}`, `{"type":"object","description":"` + strings.Repeat("x", 17*1024) + `"}`, "all_must_pass", true},
		{"array root", `{"mode":"local","verifier_id":"report"}`, `{"type":"array","items":{"type":"string"}}`, "all_must_pass", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var input domain.CompletionContract
			if err := json.Unmarshal([]byte(`{"output":`+tc.output+`,"verifiers":[{"id":"report","type":"json_schema","required":true,"config":{"schema":`+tc.schema+`}}],"policy":{"mode":"`+tc.policy+`"}}`), &input); err != nil {
				t.Fatal(err)
			}
			frozen, err := NewRegistry(Options{}).FreezeContract(&input)
			if tc.invalid {
				if !IsKind(err, ErrorInvalidContract) {
					t.Fatalf("expected invalid contract, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			encoded, _ := json.Marshal(frozen)
			var wire map[string]any
			_ = json.Unmarshal(encoded, &wire)
			output, ok := wire["output"].(map[string]any)
			if !ok || output["verifier_id"] != "report" || output["version"] != "json-schema-output-v1" {
				t.Fatalf("output contract not frozen: %s", encoded)
			}
		})
	}
}

const reportSchema = `{"type":"object","properties":{"summary":{"type":"string"},"facts":{"type":"array","items":{"type":"string"}}},"required":["summary","facts"],"additionalProperties":false}`
