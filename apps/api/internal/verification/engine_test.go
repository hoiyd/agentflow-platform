package verification

import (
	"context"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/store"
)

func TestEngineBindsEvidenceToSubjectAndPassesFreshCandidate(t *testing.T) {
	fileStore, run, engine := newVerificationTestRun(t, domain.VerificationPolicy{
		Mode: domain.VerificationAllMustPass, MaxAttempts: 2, OnExhausted: domain.VerificationFailRun,
	}, []domain.VerifierSpec{schemaSpec("schema", true, "ok")})

	first := SubjectForRunOutput(`{"status":"wrong"}`)
	decision, err := engine.Verify(context.Background(), run.ID, first)
	if err != nil {
		t.Fatalf("verify failed candidate: %v", err)
	}
	if decision.AllowCompletion || decision.Status != domain.VerificationFailed || decision.RunStatus != domain.RunFailedRecoverable || decision.Attempt != 1 {
		t.Fatalf("unexpected first decision: %#v", decision)
	}

	second := SubjectForRunOutput(`{"status":"ok"}`)
	decision, err = engine.Verify(context.Background(), run.ID, second)
	if err != nil {
		t.Fatalf("verify revised candidate: %v", err)
	}
	if !decision.AllowCompletion || decision.Status != domain.VerificationPassed || decision.Attempt != 2 || decision.SubjectHash != second.Hash {
		t.Fatalf("unexpected second decision: %#v", decision)
	}
	evidence, err := fileStore.ListVerificationEvidence(run.ID)
	if err != nil {
		t.Fatalf("list evidence: %v", err)
	}
	if len(evidence) != 3 || evidence[0].Status != domain.VerificationFailed || evidence[1].Status != domain.VerificationStale || evidence[1].SupersedesEvidenceID != evidence[0].ID || evidence[2].Status != domain.VerificationPassed {
		t.Fatalf("unexpected evidence history: %#v", evidence)
	}
	artifacts, err := fileStore.ListVerificationArtifacts(run.ID)
	if err != nil || len(artifacts) != 2 || artifacts[1].Content != second.Value || artifacts[1].ContentHash == "" {
		t.Fatalf("unexpected artifacts: %#v err=%v", artifacts, err)
	}
	stored, ok, err := fileStore.GetRun(run.ID)
	if err != nil || !ok || stored.VerificationStatus != domain.VerificationPassed {
		t.Fatalf("unexpected stored verification status: %#v ok=%v err=%v", stored, ok, err)
	}
	events, err := fileStore.ListRunEvents(run.ID)
	if err != nil {
		t.Fatalf("list run events: %v", err)
	}
	if !containsEvent(events, domain.EventVerificationStale) || !containsEvent(events, domain.EventVerificationPassed) || !containsEvent(events, domain.EventRunRevisionRequested) {
		t.Fatalf("verification lifecycle was not traced: %#v", events)
	}
}

func TestEngineEnforcesPolicyAndAttemptBudget(t *testing.T) {
	t.Run("all must pass ignores optional failure", func(t *testing.T) {
		_, run, engine := newVerificationTestRun(t, domain.VerificationPolicy{
			Mode: domain.VerificationAllMustPass, MaxAttempts: 1, OnExhausted: domain.VerificationFailRun,
		}, []domain.VerifierSpec{schemaSpec("required", true, "ok"), schemaSpec("optional", false, "different")})
		decision, err := engine.Verify(context.Background(), run.ID, SubjectForRunOutput(`{"status":"ok"}`))
		if err != nil || !decision.AllowCompletion {
			t.Fatalf("optional failure blocked completion: decision=%#v err=%v", decision, err)
		}
	})

	t.Run("any may pass accepts one required verifier", func(t *testing.T) {
		_, run, engine := newVerificationTestRun(t, domain.VerificationPolicy{
			Mode: domain.VerificationAnyMayPass, MaxAttempts: 1, OnExhausted: domain.VerificationFailRun,
		}, []domain.VerifierSpec{schemaSpec("first", true, "ok"), schemaSpec("second", true, "different")})
		decision, err := engine.Verify(context.Background(), run.ID, SubjectForRunOutput(`{"status":"ok"}`))
		if err != nil || !decision.AllowCompletion {
			t.Fatalf("any_may_pass rejected one pass: decision=%#v err=%v", decision, err)
		}
	})

	t.Run("exhausted policy waits for user", func(t *testing.T) {
		_, run, engine := newVerificationTestRun(t, domain.VerificationPolicy{
			Mode: domain.VerificationAllMustPass, MaxAttempts: 1, OnExhausted: domain.VerificationWaitForUser,
		}, []domain.VerifierSpec{schemaSpec("schema", true, "ok")})
		decision, err := engine.Verify(context.Background(), run.ID, SubjectForRunOutput(`{"status":"wrong"}`))
		if err != nil || decision.AllowCompletion || decision.RunStatus != domain.RunWaitingForUser {
			t.Fatalf("unexpected exhausted decision: %#v err=%v", decision, err)
		}
	})
}

func TestEngineDoesNotRequireVerificationWithoutContract(t *testing.T) {
	fileStore, err := store.NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	conversation, _ := fileStore.CreateConversation("no verification")
	run, err := fileStore.CreateRun("agent_planner", conversation.ID, verificationSnapshot())
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	engine := NewEngine(fileStore, NewRegistry(Options{}))
	decision, err := engine.Verify(context.Background(), run.ID, SubjectForRunOutput("plain response"))
	if err != nil || !decision.AllowCompletion || decision.Status != domain.VerificationNotRequired {
		t.Fatalf("unexpected no-contract decision: %#v err=%v", decision, err)
	}
}

func TestEngineFailsClosedWhenFrozenVerifierImplementationIsUnavailable(t *testing.T) {
	fileStore, run, engine := newVerificationTestRun(t, domain.VerificationPolicy{
		Mode: domain.VerificationAllMustPass, MaxAttempts: 1, OnExhausted: domain.VerificationFailRun,
	}, []domain.VerifierSpec{schemaSpec("schema", true, "ok")})
	delete(engine.registry.verifiers, domain.VerifierJSONSchema)
	decision, err := engine.Verify(context.Background(), run.ID, SubjectForRunOutput(`{"status":"ok"}`))
	if err != nil || decision.AllowCompletion || decision.Status != domain.VerificationBlocked {
		t.Fatalf("unavailable verifier did not fail closed: decision=%#v err=%v", decision, err)
	}
	evidence, err := fileStore.ListVerificationEvidence(run.ID)
	if err != nil || len(evidence) != 1 || evidence[0].Status != domain.VerificationBlocked {
		t.Fatalf("blocked evidence was not persisted: %#v err=%v", evidence, err)
	}
}

func newVerificationTestRun(t *testing.T, policy domain.VerificationPolicy, specs []domain.VerifierSpec) (*store.FileStore, domain.Run, *Engine) {
	t.Helper()
	fileStore, err := store.NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	registry := NewRegistry(Options{})
	contract, err := registry.FreezeContract(&domain.CompletionContract{ID: "contract_test", Verifiers: specs, Policy: policy})
	if err != nil {
		t.Fatalf("freeze contract: %v", err)
	}
	conversation, err := fileStore.CreateConversation("verification")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	run, err := fileStore.CreateRunWithContract("agent_planner", conversation.ID, verificationSnapshot(), contract)
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	return fileStore, run, NewEngine(fileStore, registry)
}

func schemaSpec(id string, required bool, expected string) domain.VerifierSpec {
	return domain.VerifierSpec{
		ID: id, Type: domain.VerifierJSONSchema, Required: required,
		JSONSchema: &domain.JSONSchemaVerifierConfig{Schema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"status": map[string]any{"const": expected}},
			"required":   []any{"status"},
		}},
	}
}

func verificationSnapshot() domain.RuntimeSnapshot {
	return domain.RuntimeSnapshot{
		SchemaVersion: domain.CurrentRuntimeSnapshotVersion, Mode: "single",
		Agent:           domain.RuntimeAgentSnapshot{ID: "agent_planner", Executor: domain.DefaultAgentExecutor},
		Model:           domain.RuntimeModelSnapshot{Provider: "local", Model: "test"},
		ContextAssembly: domain.ContextAssemblyConfig{AssemblerVersion: "context-assembler-v1"},
	}
}

func containsEvent(events []domain.RunEvent, eventType domain.RunEventType) bool {
	for _, event := range events {
		if event.Type == eventType {
			return true
		}
	}
	return false
}
