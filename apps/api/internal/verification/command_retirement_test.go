package verification

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/testsupport/fixturestore"
)

// Failure inventory: new contracts can still select command; a retired optional
// check or any_may_pass gate bypasses retirement; rejected execution mutates
// existing Run/Evidence; historical command output becomes unreadable.
func TestHostCommandVerifierRetirement(t *testing.T) {
	registry := NewRegistry(Options{})
	if slices.Contains(registry.Types(), domain.VerifierCommand) {
		t.Error("host command remains registered")
	}
	for _, required := range []bool{true, false} {
		for _, mode := range []domain.VerificationPolicyMode{domain.VerificationAllMustPass, domain.VerificationAnyMayPass} {
			t.Run(string(mode)+"/required="+map[bool]string{true: "true", false: "false"}[required], func(t *testing.T) {
				contract := &domain.CompletionContract{ID: "historical-command", Version: domain.CurrentCompletionContractVersion, SubjectType: "run_output",
					Verifiers: []domain.VerifierSpec{
						{ID: "host", Type: domain.VerifierCommand, Version: "command-v1", Required: required, TimeoutMS: 1000, Config: map[string]any{"args": []string{"/usr/bin/true"}, "working_directory": "."}},
						{ID: "text", Type: domain.VerifierTextConstraints, Version: "text-constraints-v1", Required: true, TimeoutMS: 1000, Config: map[string]any{"min_characters": 1}},
					}, Policy: domain.VerificationPolicy{Mode: mode, MaxAttempts: 2, OnExhausted: domain.VerificationWaitForUser}}
				if _, err := registry.FreezeContract(contract); !IsKind(err, ErrorInvalidContract) || !strings.Contains(err.Error(), "retired") {
					t.Errorf("new command contract accepted: %v", err)
				}
				var err error
				contract.Hash, err = completionContractHash(*contract)
				if err != nil {
					t.Fatal(err)
				}
				storage := fixturestore.New()
				conversation, err := storage.CreateConversation("historical verification")
				if err != nil {
					t.Fatal(err)
				}
				run, err := storage.CreateRunWithContract("agent_planner", conversation.ID, verificationSnapshot(), contract)
				if err != nil {
					t.Fatal(err)
				}
				record := domain.VerificationRecord{Evidence: domain.VerificationEvidence{ID: "historical-evidence", RunID: run.ID, VerifierType: domain.VerifierCommand, VerifierVersion: "command-v1", Status: domain.VerificationPassed, Attempt: 1, ArtifactIDs: []string{"historical-output"}},
					Artifacts: []domain.VerificationArtifact{{ID: "historical-output", RunID: run.ID, EvidenceID: "historical-evidence", Kind: "command_output", Content: "old test output"}}}
				if err := storage.AppendVerificationRecord(record); err != nil {
					t.Fatal(err)
				}
				before, _, _ := storage.GetRun(run.ID)
				decision, err := NewEngine(storage, registry).Verify(t.Context(), run.ID, SubjectForRunOutput("valid answer"))
				if !IsKind(err, ErrorInvalidContract) || decision.AllowCompletion {
					t.Errorf("historical command executed: %+v %v", decision, err)
				}
				after, _, _ := storage.GetRun(run.ID)
				evidence, err := storage.ListVerificationEvidence(run.ID)
				if err != nil {
					t.Fatal(err)
				}
				artifacts, err := storage.ListVerificationArtifacts(run.ID)
				if err != nil {
					t.Fatal(err)
				}
				events, err := storage.ListRunEvents(run.ID)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(before, after) || len(events) != 0 || len(evidence) != 1 || evidence[0].ID != record.Evidence.ID || len(artifacts) != 1 || artifacts[0].Content != "old test output" {
					t.Fatalf("retirement mutated historical state: run=%+v evidence=%+v artifacts=%+v events=%+v", after, evidence, artifacts, events)
				}
			})
		}
	}
}
