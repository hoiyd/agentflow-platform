package app

import (
	"context"
	"database/sql"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/failure"
	"agentflow-platform/apps/api/internal/identity"
	"agentflow-platform/apps/api/internal/inference/requestcontrol"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/testsupport/pgfixture"
)

// Authoritative Postgres ownership, including auxiliary background work and
// Resume after access revocation. Never invent an owner for orphaned work.
func TestModelOwnerResolutionPostgres(t *testing.T) {
	url := pgfixture.DatabaseURL(t)
	s, err := store.NewPostgresStore(url)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.InitializeWorkspaceLifecycle(t.Context()); err != nil {
		t.Fatal(err)
	}
	user := identity.User{ID: identity.UserID("https://fixture.invalid", "owner"), Issuer: "https://fixture.invalid", Subject: "owner"}
	if err := s.UpsertIdentity(t.Context(), user); err != nil {
		t.Fatal(err)
	}
	w, err := s.CreateWorkspace(t.Context(), user.ID, "Owner space", "")
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.CreateConversationInWorkspace(w.ID, "test")
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.CreateRunWithContract("agent_planner", c.ID, domain.RuntimeSnapshot{SchemaVersion: domain.CurrentRuntimeSnapshotVersion, RunBudget: &domain.RuntimeRunBudget{}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	resolve := modelOwnerResolver(s)
	background := event.WithScope(context.Background(), event.Scope{RunID: run.ID})
	if owner, err := resolve(background); err != nil || owner != user.ID {
		t.Fatalf("background identity: %q %v", owner, err)
	}
	if _, err := resolve(requestcontrol.WithOwner(background, "foreign")); failure.Describe(err).Code != "model_owner_unavailable" {
		t.Fatalf("foreign identity: %v", err)
	}
	if owner, err := resolve(requestcontrol.WithOwner(context.Background(), user.ID)); err != nil || owner != user.ID {
		t.Fatalf("HTTP identity: %q %v", owner, err)
	}
	if _, err := resolve(context.Background()); failure.Describe(err).Code != "model_owner_required" {
		t.Fatalf("missing identity: %v", err)
	}
	if _, err := resolve(event.WithScope(context.Background(), event.Scope{RunID: "missing"})); failure.Describe(err).Code != "model_owner_unavailable" {
		t.Fatalf("missing run: %v", err)
	}
	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`DELETE FROM auth_memberships WHERE user_id=$1 AND workspace_id=$2`, user.ID, w.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := resolve(background); failure.Describe(err).Code != "model_owner_unavailable" {
		t.Fatalf("revoked auxiliary/Resume access: %v", err)
	}
	if _, err := s.WorkspaceModelOwner(t.Context(), "invalid"); !store.IsNotFound(err) {
		t.Fatal(err)
	}
	if _, err := s.WorkspaceModelOwner(t.Context(), "99999999"); !store.IsNotFound(err) {
		t.Fatal(err)
	}
	s.Close()
	if _, err := resolve(background); failure.Describe(err).Code != "model_owner_resolution_failed" {
		t.Fatalf("storage failure: %v", err)
	}
	ctx, cancel := context.WithCancel(background)
	cancel()
	if _, err := resolve(ctx); err != context.Canceled {
		t.Fatalf("canceled lookup: %v", err)
	}
}
