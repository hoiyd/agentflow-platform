package store

import (
	"errors"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
)

func TestFileStoreAdministrativeMutationsAndLists(t *testing.T) {
	fileStore, err := NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	if conversations, err := fileStore.ListConversations(); err != nil || len(conversations) != 0 {
		t.Fatalf("expected empty conversations: items=%#v err=%v", conversations, err)
	}

	conversation, err := fileStore.CreateConversation("Initial title")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	if err := fileStore.UpdateConversationTitle(conversation.ID, " Updated title "); err != nil {
		t.Fatalf("update conversation title: %v", err)
	}
	conversations, err := fileStore.ListConversations()
	if err != nil || len(conversations) != 1 || conversations[0].Title != "Updated title" {
		t.Fatalf("unexpected conversations: items=%#v err=%v", conversations, err)
	}

	agent, err := fileStore.CreateAgent(domain.Agent{Name: "Coverage agent", SystemPrompt: "Original prompt"})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	agent.Description = "Updated description"
	agent.SystemPrompt = "Updated prompt"
	updatedAgent, err := fileStore.UpdateAgent(agent)
	if err != nil || updatedAgent.Description != "Updated description" || updatedAgent.SystemPrompt != "Updated prompt" {
		t.Fatalf("update agent: agent=%#v err=%v", updatedAgent, err)
	}

	snapshot := testRuntimeSnapshot()
	snapshot.Agent.ID = agent.ID
	run, err := fileStore.CreateRunWithContract(agent.ID, conversation.ID, snapshot, nil)
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	heartbeatRun, err := fileStore.UpdateRunHeartbeat(run.ID)
	if err != nil || heartbeatRun.HeartbeatAt == nil {
		t.Fatalf("update heartbeat: run=%#v err=%v", heartbeatRun, err)
	}
	runs, err := fileStore.ListRuns()
	if err != nil || len(runs) != 1 || runs[0].ID != run.ID {
		t.Fatalf("unexpected runs: items=%#v err=%v", runs, err)
	}
	ledger, ok, err := fileStore.GetRunUsageLedger(run.ID)
	if err != nil || !ok || ledger.RunID != run.ID || len(ledger.Entries) != 0 {
		t.Fatalf("unexpected empty ledger: ledger=%#v ok=%t err=%v", ledger, ok, err)
	}

	step, err := fileStore.CreateCollaborationStep(domain.CollaborationStep{
		RunID: run.ID, ConversationID: conversation.ID, AgentID: agent.ID, Role: "plan", Input: "task",
	})
	if err != nil {
		t.Fatalf("create collaboration step: %v", err)
	}
	step, err = fileStore.UpdateCollaborationStepOutput(step.ID, " draft output ")
	if err != nil || step.Output != "draft output" {
		t.Fatalf("update step output: step=%#v err=%v", step, err)
	}
	step, err = fileStore.UpdateCollaborationStep(step.ID, domain.CollaborationStepCompleted, " final output ", "")
	if err != nil || step.Status != domain.CollaborationStepCompleted || step.Output != "final output" {
		t.Fatalf("complete step: step=%#v err=%v", step, err)
	}
	steps, err := fileStore.ListCollaborationSteps(run.ID)
	if err != nil || len(steps) != 1 || steps[0].ID != step.ID {
		t.Fatalf("unexpected steps: items=%#v err=%v", steps, err)
	}

	now := time.Now().UTC()
	record := domain.VerificationRecord{
		Evidence: domain.VerificationEvidence{
			ID: "evidence-1", RunID: run.ID, ContractID: "contract-1", VerifierID: "verifier-1",
			Status: domain.VerificationPassed, StartedAt: now, CompletedAt: now, ArtifactIDs: []string{"artifact-1"},
		},
		Artifacts: []domain.VerificationArtifact{{
			ID: "artifact-1", RunID: run.ID, EvidenceID: "evidence-1", Kind: "log", Content: "passed", CreatedAt: now,
		}},
	}
	if err := fileStore.AppendVerificationRecord(record); err != nil {
		t.Fatalf("append verification record: %v", err)
	}
	artifacts, err := fileStore.ListVerificationArtifacts(run.ID)
	if err != nil || len(artifacts) != 1 || artifacts[0].ID != "artifact-1" {
		t.Fatalf("unexpected artifacts: items=%#v err=%v", artifacts, err)
	}
}

func TestFileStoreAdministrativeMutationsRejectMissingIDs(t *testing.T) {
	fileStore, err := NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	if err := fileStore.UpdateConversationTitle("missing", "title"); err == nil {
		t.Fatal("expected missing conversation error")
	}
	if _, err := fileStore.UpdateRunHeartbeat("missing"); err == nil {
		t.Fatal("expected missing run error")
	}
	if _, err := fileStore.UpdateCollaborationStep("missing", domain.CollaborationStepFailed, "", "failed"); err == nil {
		t.Fatal("expected missing collaboration step error")
	}
	if _, err := fileStore.UpdateCollaborationStepOutput("missing", "output"); err == nil {
		t.Fatal("expected missing collaboration output error")
	}
	if _, ok, err := fileStore.GetRunUsageLedger("missing"); err != nil || ok {
		t.Fatalf("expected missing ledger: ok=%t err=%v", ok, err)
	}
	notFound := ErrNotFound("run")
	if notFound.Error() != "run not found" || !IsNotFound(errors.Join(errors.New("wrapped"), notFound)) || IsNotFound(errors.New("other")) {
		t.Fatal("not-found helpers did not preserve typed errors")
	}
}
