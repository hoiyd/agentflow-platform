package store

import (
	"testing"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/testsupport/pgfixture"
)

func TestWorkspaceAgentsIsolationAndTemplates(t *testing.T) {
	s, err := NewPostgresStore(pgfixture.DatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	a := s.ForWorkspace(domain.NewWorkspaceScope(testOwnedWorkspace(t, s)))
	b := s.ForWorkspace(domain.NewWorkspaceScope(testOwnedWorkspace(t, s)))
	created, err := a.CreateAgent(domain.Agent{Name: "Private writer", SystemPrompt: "Private instructions", Tools: []string{"calculator"}})
	if err != nil {
		t.Fatal(err)
	}
	if created.WorkspaceID == "" || created.IsTemplate {
		t.Fatalf("invalid ownership: %#v", created)
	}
	if _, ok, err := b.GetAgent(created.ID); err != nil || ok {
		t.Fatalf("foreign read: %v %v", ok, err)
	}
	if _, err := b.UpdateAgent(created); !IsNotFound(err) {
		t.Fatalf("foreign update: %v", err)
	}
	if err := b.ArchiveAgent(created.ID); !IsNotFound(err) {
		t.Fatalf("foreign archive: %v", err)
	}
	conversation, err := s.CreateConversationInWorkspace(created.WorkspaceID, "Agent assignment boundary")
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.CreateRunWithContract("agent_planner", conversation.ID, testRuntimeSnapshot(), nil)
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := b.CreateAgent(domain.Agent{Name: "Foreign Agent"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpdateRunAgent(run.ID, foreign.ID); !IsNotFound(err) {
		t.Fatalf("foreign Run Agent assignment: %v", err)
	}
	if _, err = s.UpdateRunAgent(run.ID, created.ID); err != nil {
		t.Fatal(err)
	}
	items, err := b.ListAgents()
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.ID == created.ID {
			t.Fatal("foreign Agent leaked")
		}
	}
	template, ok, err := a.GetAgent("agent_planner")
	if err != nil || !ok || !template.IsTemplate {
		t.Fatalf("template missing: %#v %v", template, err)
	}
	if _, err := a.UpdateAgent(template); err == nil {
		t.Fatal("template was mutable")
	}
	if err := a.ArchiveAgent(template.ID); err == nil {
		t.Fatal("template was archivable")
	}
	created.Name = "Renamed"
	updated, err := a.UpdateAgent(created)
	if err != nil || updated.Name != "Renamed" || updated.WorkspaceID != created.WorkspaceID {
		t.Fatalf("update: %#v %v", updated, err)
	}
	if err := a.ArchiveAgent(created.ID); err != nil {
		t.Fatal(err)
	}
	if item, ok, err := a.GetAgent(created.ID); err != nil || !ok || !item.Archived {
		t.Fatalf("archive: %#v %v", item, err)
	}
}
