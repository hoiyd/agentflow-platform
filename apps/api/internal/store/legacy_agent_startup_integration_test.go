package store

import (
	"testing"

	"agentflow-platform/apps/api/internal/testsupport/pgfixture"
)

func TestLegacyAgentStartupPreservesPrivateDefaultEdits(t *testing.T) {
	url := pgfixture.DatabaseURL(t)
	s, err := NewPostgresStore(url)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`UPDATE agents SET is_template=false,name='Private legacy planner',system_prompt='Do not publish or replace' WHERE id='agent_planner'`); err != nil {
		t.Fatal(err)
	}
	// Simulate the deployed schema before PROD-018, not only a fresh database.
	if _, err = s.db.Exec(`ALTER TABLE agents DROP COLUMN is_template`); err != nil {
		t.Fatal(err)
	}
	workspace := testOwnedWorkspace(t, s)
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = NewPostgresStore(url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	original, ok, err := s.GetAgent("agent_planner")
	if err != nil || !ok || original.IsTemplate || original.SystemPrompt != "Do not publish or replace" {
		t.Fatalf("legacy content overwritten: %#v %v", original, err)
	}
	visible, err := s.ListAgentsByWorkspace(workspace)
	if err != nil {
		t.Fatal(err)
	}
	foundTemplate := false
	for _, item := range visible {
		if item.ID == original.ID || item.SystemPrompt == original.SystemPrompt {
			t.Fatal("unassigned private Agent exposed")
		}
		if item.ID == "template_planner" && item.IsTemplate {
			foundTemplate = true
		}
	}
	if !foundTemplate {
		t.Fatal("safe shared template missing")
	}
}
