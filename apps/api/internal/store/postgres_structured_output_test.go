package store

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/testsupport/pgfixture"
	"agentflow-platform/apps/api/internal/verification"
)

func TestPostgresStructuredOutputSurvivesRestartAndScope(t *testing.T) {
	url := pgfixture.DatabaseURL(t)
	storage, err := NewPostgresStore(url)
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Close()
	contract, err := verification.NewRegistry(verification.Options{}).FreezeContract(&domain.CompletionContract{Output: &domain.StructuredOutputConfig{Mode: "local", VerifierID: "report"}, Verifiers: []domain.VerifierSpec{{ID: "report", Type: domain.VerifierJSONSchema, Required: true, Config: map[string]any{"schema": map[string]any{"type": "object"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := storage.CreateConversation("structured report roundtrip")
	if err != nil {
		t.Fatal(err)
	}
	run, err := storage.CreateRunWithContract("agent_planner", conversation.ID, testRuntimeSnapshot(), contract)
	if err != nil {
		t.Fatal(err)
	}
	_, err = storage.UpdateRunStatus(run.ID, domain.RunRunning, "")
	if err != nil {
		t.Fatal(err)
	}
	const output = `{"id":"9007199254740993","summary":"evidence"}`
	decision, err := verification.NewEngine(storage, verification.NewRegistry(verification.Options{})).Verify(context.Background(), run.ID, verification.SubjectForRunOutput(output))
	if err != nil || !decision.AllowCompletion {
		t.Fatalf("verify: %+v %v", decision, err)
	}
	_, err = storage.UpdateRunStatus(run.ID, domain.RunCompleted, "")
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := NewPostgresStore(url)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	frozen, ok, err := reopened.GetRun(run.ID)
	if err != nil || !ok {
		t.Fatalf("reload: %v", err)
	}
	evidence, err := reopened.ListVerificationEvidence(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	artifacts, err := reopened.ListVerificationArtifacts(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	result := verification.StructuredResult(frozen, evidence, artifacts)
	encoded, _ := json.Marshal(result)
	if result.ContractHash != contract.Hash || result.Version != domain.StructuredOutputVersion || !strings.Contains(string(encoded), `"id":"9007199254740993"`) {
		t.Fatalf("result drift: %s", encoded)
	}
	if _, err := reopened.ForWorkspace(domain.NewWorkspaceScope("999999999")).ListVerificationArtifacts(run.ID); err == nil {
		t.Fatal("artifact crossed workspace")
	}
	// A bounded report must not be advertised when its stored Artifact cannot be
	// recovered intact, even if the schema verification itself passed.
	artifacts[0].Truncated = true
	if len(verification.StructuredResult(frozen, evidence, artifacts).Value) != 0 {
		t.Fatal("truncated output exposed")
	}
	largeContract, err := verification.NewRegistry(verification.Options{}).FreezeContract(contract)
	if err != nil {
		t.Fatal(err)
	}
	largeRun, err := storage.CreateRunWithContract("agent_planner", conversation.ID, testRuntimeSnapshot(), largeContract)
	if err != nil {
		t.Fatal(err)
	}
	decision, err = verification.NewEngine(storage, verification.NewRegistry(verification.Options{})).Verify(context.Background(), largeRun.ID, verification.SubjectForRunOutput(`{"summary":"`+strings.Repeat("x", domain.MaxStructuredOutputBytes)+`"}`))
	if err != nil || decision.AllowCompletion {
		t.Fatalf("large result passed: %+v %v", decision, err)
	}
	records, err := storage.ListVerificationEvidence(largeRun.ID)
	if err != nil || len(records) != 1 || records[0].Details["reason_code"] != "output_too_large" {
		t.Fatalf("limit evidence lost: %+v %v", records, err)
	}
}
