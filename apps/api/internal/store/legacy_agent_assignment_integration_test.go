package store

import (
	"testing"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/testsupport/pgfixture"
)

func TestLegacyAgentAssignmentPreservesProfilesAndHistory(t *testing.T) {
	s, err := NewPostgresStore(pgfixture.DatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	a, b := testOwnedWorkspace(t, s), testOwnedWorkspace(t, s)
	original, err := s.CreateAgent(domain.Agent{WorkspaceID: a, Name: "Private legacy", SystemPrompt: "Preserve these instructions", Tools: []string{"calculator"}, Skills: []string{"writing-method"}})
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := s.CreateConversationInWorkspace(a, "History")
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.CreateRunWithContract(original.ID, conversation.ID, testRuntimeSnapshot(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpdateRunStatus(run.ID, domain.RunCompleted, ""); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`UPDATE agents SET workspace_id=NULL WHERE id=$1`, original.ID); err != nil {
		t.Fatal(err)
	}
	preview, err := s.AssignLegacyAgents(t.Context(), a, b, false)
	if err != nil || len(preview) != 1 {
		t.Fatalf("preview: %#v %v", preview, err)
	}
	before, _, _ := s.GetAgent(original.ID)
	if before.WorkspaceID != "" {
		t.Fatal("preview assigned ownership")
	}
	if _, err = s.AssignLegacyAgents(t.Context(), a, "999999999", true); err == nil {
		t.Fatal("invalid target accepted")
	}
	applied, err := s.AssignLegacyAgents(t.Context(), a, b, true)
	if err != nil || len(applied) != 1 {
		t.Fatalf("apply: %#v %v", applied, err)
	}
	copy, ok, err := s.GetAgentInWorkspace(b, applied[0].CopyID)
	if err != nil || !ok || copy.ID == original.ID || copy.SystemPrompt != original.SystemPrompt || len(copy.Skills) != 1 {
		t.Fatalf("copy: %#v %v", copy, err)
	}
	source, _, _ := s.GetAgent(original.ID)
	if source.WorkspaceID != a || source.SystemPrompt != original.SystemPrompt {
		t.Fatal("source changed")
	}
	retained, _, err := s.GetRun(run.ID)
	if err != nil || retained.AgentID != original.ID {
		t.Fatal("historical reference changed")
	}
	again, err := s.AssignLegacyAgents(t.Context(), a, b, true)
	if err != nil || len(again) != 0 {
		t.Fatalf("repeat: %#v %v", again, err)
	}
}

func TestLegacyAgentAssignmentRejectsMissingTargetsAndActiveRuns(t *testing.T) {
	s, err := NewPostgresStore(pgfixture.DatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	a := testOwnedWorkspace(t, s)
	for _, targets := range [][2]string{{"", ""}, {a, a}, {"99999999", ""}} {
		if _, err := s.AssignLegacyAgents(t.Context(), targets[0], targets[1], true); err == nil {
			t.Fatal("invalid targets accepted")
		}
	}
	conversation, err := s.CreateConversationInWorkspace(a, "Busy")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateRunWithContract("agent_planner", conversation.ID, testRuntimeSnapshot(), nil); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AssignLegacyAgents(t.Context(), a, "", true); err == nil {
		t.Fatal("active execution accepted")
	}
}

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

func TestLegacyAgentAssignmentRollsBackPartialCopies(t *testing.T) {
	s, err := NewPostgresStore(pgfixture.DatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	a, b := testOwnedWorkspace(t, s), testOwnedWorkspace(t, s)
	for _, id := range []string{"legacy-a", "legacy-b"} {
		if _, err = s.CreateAgent(domain.Agent{ID: id, Name: id}); err != nil {
			t.Fatal(err)
		}
	}
	// Fail the second copy after the first Agent was already copied and assigned.
	if _, err = s.db.Exec(`CREATE FUNCTION fail_second_copy() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.id<>'legacy-b' AND NEW.name='legacy-b' THEN RAISE EXCEPTION 'injected copy failure'; END IF; RETURN NEW; END $$;
 CREATE TRIGGER fail_second_copy BEFORE INSERT ON agents FOR EACH ROW EXECUTE FUNCTION fail_second_copy()`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AssignLegacyAgents(t.Context(), a, b, true); err == nil {
		t.Fatal("injected failure did not abort migration")
	}
	for _, id := range []string{"legacy-a", "legacy-b"} {
		item, _, err := s.GetAgent(id)
		if err != nil || item.WorkspaceID != "" {
			t.Fatalf("partial assignment survived: %#v %v", item, err)
		}
	}
	var copies int
	if err = s.db.QueryRow(`SELECT COUNT(*) FROM agents WHERE workspace_id=$1`, b).Scan(&copies); err != nil || copies != 0 {
		t.Fatalf("partial copies survived: %d %v", copies, err)
	}
}
