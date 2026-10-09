package store

import (
	"reflect"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/identity"
	"agentflow-platform/apps/api/internal/testsupport/pgfixture"
)

func TestWorkspaceToolConfigRoundTripAndExplicitEmpty(t *testing.T) {
	url := pgfixture.DatabaseURL(t)
	s, err := NewPostgresStore(url)
	if err != nil {
		t.Fatal(err)
	}
	workspace := testOwnedWorkspace(t, s)
	initial, err := s.EnsureWorkspaceToolConfig(workspace, []string{"calculator", "get_current_time"})
	if err != nil {
		t.Fatal(err)
	}
	if initial.Revision != 1 || len(initial.AllowedTools) != 2 {
		t.Fatalf("initial: %#v", initial)
	}
	for _, name := range initial.AllowedTools {
		if err := s.SetWorkspaceToolEnabled(workspace, name, false); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = NewPostgresStore(url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	saved, err := s.EnsureWorkspaceToolConfig(workspace, []string{"calculator", "new_tool"})
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.AllowedTools) != 0 || saved.Revision != 3 {
		t.Fatalf("empty reset on reopen: %#v", saved)
	}
	if err := s.SetWorkspaceToolEnabled(workspace, "calculator", true); err != nil {
		t.Fatal(err)
	}
	if err := s.SetWorkspaceToolEnabled(workspace, "calculator", true); err != nil {
		t.Fatal(err)
	}
	saved, err = s.EnsureWorkspaceToolConfig(workspace, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(saved.AllowedTools, []string{"calculator"}) {
		t.Fatalf("duplicate grants: %#v", saved)
	}
}

func TestArchivedWorkspaceToolReadDoesNotInitializeGrants(t *testing.T) {
	s, err := NewPostgresStore(pgfixture.DatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	workspace := testOwnedWorkspace(t, s)
	archived := "archived"
	if _, err = s.UpdateWorkspace(t.Context(), identity.SuperUserID, workspace, domain.WorkspaceUpdate{Status: &archived}, false); err != nil {
		t.Fatal(err)
	}
	config, err := s.EnsureWorkspaceToolConfig(workspace, []string{"calculator"})
	if err != nil || len(config.AllowedTools) != 0 {
		t.Fatalf("archived read: %#v %v", config, err)
	}
	var count int
	if err = s.db.QueryRow(`SELECT COUNT(*) FROM workspace_tool_config WHERE workspace_id=$1`, workspace).Scan(&count); err != nil || count != 0 {
		t.Fatalf("read wrote grants: %d %v", count, err)
	}
	if err = s.CheckWorkspaceExecution(workspace); err == nil {
		t.Fatal("archived Workspace permitted execution")
	}
}

func TestWorkspaceToolConfigRejectsMissingWorkspaceAndMissingGrantState(t *testing.T) {
	s, err := NewPostgresStore(pgfixture.DatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if _, err = s.EnsureWorkspaceToolConfig("999999999", []string{"calculator"}); !IsNotFound(err) {
		t.Fatalf("missing Workspace: %v", err)
	}
	if err = s.CheckWorkspaceExecution("999999999"); !IsNotFound(err) {
		t.Fatalf("missing Workspace execution: %v", err)
	}
	workspace := testOwnedWorkspace(t, s)
	if err = s.SetWorkspaceToolEnabled(workspace, "calculator", true); !IsNotFound(err) {
		t.Fatalf("missing configuration: %v", err)
	}
}
