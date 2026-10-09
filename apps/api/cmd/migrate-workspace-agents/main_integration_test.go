package main

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/identity"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/testsupport/pgfixture"
)

// Exercise the operator command against Postgres, not a mocked migration.
// Invalid flags/targets must reject, preview must not assign, apply must retain
// original content and IDs, and a repeated application must not duplicate copies.
func TestMigrationCommandPostgres(t *testing.T) {
	url := pgfixture.DatabaseURL(t)
	s, err := store.NewPostgresStore(url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	a, err := s.CreateWorkspace(t.Context(), identity.SuperUserID, "Primary", "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.CreateWorkspace(t.Context(), identity.SuperUserID, "Copy", "")
	if err != nil {
		t.Fatal(err)
	}
	original, err := s.CreateAgent(domain.Agent{Name: "Legacy private profile", SystemPrompt: "Retain content"})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DATABASE_URL", url)
	arguments := []string{"--workspace", a.ID, "--copy-workspace", b.ID}
	var out, stderr bytes.Buffer
	if code := run(t.Context(), arguments, &out, &stderr); code != 0 {
		t.Fatalf("preview: %d %s", code, stderr.String())
	}
	var preview struct {
		Applied bool                          `json:"applied"`
		Agents  []store.LegacyAgentAssignment `json:"agents"`
	}
	if err = json.Unmarshal(out.Bytes(), &preview); err != nil || preview.Applied || len(preview.Agents) != 1 {
		t.Fatalf("preview: %s %v", out.String(), err)
	}
	before, _, err := s.GetAgent(original.ID)
	if err != nil || before.WorkspaceID != "" {
		t.Fatal("preview assigned ownership")
	}
	out.Reset()
	if code := run(t.Context(), append(arguments, "--apply"), &out, &stderr); code != 0 {
		t.Fatalf("apply: %d %s", code, stderr.String())
	}
	assigned, _, err := s.GetAgent(original.ID)
	if err != nil || assigned.WorkspaceID != a.ID || assigned.SystemPrompt != original.SystemPrompt {
		t.Fatalf("assignment: %#v %v", assigned, err)
	}
	if code := run(t.Context(), append(arguments, "--apply"), &out, &stderr); code != 0 {
		t.Fatalf("repeat: %d %s", code, stderr.String())
	}
	items, err := s.ListAgentsByWorkspace(b.ID)
	if err != nil {
		t.Fatal(err)
	}
	copies := 0
	for _, item := range items {
		if !item.IsTemplate {
			copies++
		}
	}
	if copies != 1 {
		t.Fatalf("duplicate copies: %d", copies)
	}
	for _, args := range [][]string{nil, {"--unknown"}, {"--workspace", a.ID, "extra"}, {"--workspace", "999999999"}} {
		if code := run(context.Background(), args, &out, &stderr); code == 0 {
			t.Fatalf("invalid command accepted: %v", args)
		}
	}
	if code := run(t.Context(), []string{"--help"}, &out, &stderr); code != 0 {
		t.Fatalf("help: %d", code)
	}
	t.Setenv("DATABASE_URL", "invalid-connection-string")
	if code := run(t.Context(), arguments, &out, &stderr); code != 1 {
		t.Fatalf("unavailable database: %d", code)
	}
}
