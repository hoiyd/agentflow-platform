package store_test

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/identity"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/testsupport/pgfixture"
)

func workspaceDatabase(t *testing.T) (*store.PostgresStore, *sql.DB, string) {
	t.Helper()
	url := pgfixture.DatabaseURL(t)
	s, err := store.NewPostgresStore(url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return s, db, url
}
func workspaceUser(t *testing.T, s *store.PostgresStore, subject string) string {
	t.Helper()
	u := identity.User{ID: identity.UserID("https://fixture.invalid", subject), Issuer: "https://fixture.invalid", Subject: subject}
	if err := s.UpsertIdentity(t.Context(), u); err != nil {
		t.Fatal(err)
	}
	return u.ID
}

func TestWorkspaceLifecyclePostgres(t *testing.T) {
	s, db, url := workspaceDatabase(t)
	ctx := t.Context()
	if err := s.InitializeWorkspaceLifecycle(ctx); err != nil {
		t.Fatal(err)
	}
	owner, foreign := workspaceUser(t, s, "owner"), workspaceUser(t, s, "other")
	first, err := s.CreateWorkspace(ctx, owner, "First space", "Readable description")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = strconv.ParseInt(first.ID, 10, 64); err != nil || !first.IsDefault {
		t.Fatalf("identity/default: %+v %v", first, err)
	}
	if _, err = s.UpdateWorkspace(ctx, owner, first.ID, domain.WorkspaceUpdate{}, true); err == nil {
		t.Fatal("deleted last active Workspace")
	}
	second, err := s.CreateWorkspace(ctx, owner, "Second space", "")
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := s.CreateConversationInWorkspace(first.ID, "Persistent resource")
	if err != nil {
		t.Fatal(err)
	}
	name := "Renamed workspace"
	renamed, err := s.UpdateWorkspace(ctx, owner, first.ID, domain.WorkspaceUpdate{Name: &name}, false)
	if err != nil || renamed.ID != first.ID {
		t.Fatalf("rename: %+v %v", renamed, err)
	}
	for _, change := range []domain.WorkspaceUpdate{{Name: ptr("")}, {Name: ptr(strings.Repeat("x", 81))}, {Description: ptr(strings.Repeat("x", 2001))}, {Status: ptr("deleted")}} {
		if _, err = s.UpdateWorkspace(ctx, owner, first.ID, change, false); err == nil {
			t.Fatal("invalid input accepted")
		}
	}
	if _, err = s.UpdateWorkspace(ctx, foreign, first.ID, domain.WorkspaceUpdate{MakeDefault: true}, false); !store.IsNotFound(err) {
		t.Fatalf("foreign default: %v", err)
	}
	if _, err = s.GetWorkspace(ctx, foreign, first.ID); !store.IsNotFound(err) {
		t.Fatalf("foreign read: %v", err)
	}
	if _, err = db.Exec(`INSERT INTO auth_memberships(user_id,workspace_id) VALUES($1,$2)`, foreign, first.ID); err == nil {
		t.Fatal("cross-owner Membership bypassed database FK")
	}
	if _, err = s.UpdateWorkspace(ctx, owner, first.ID, domain.WorkspaceUpdate{Status: ptr("archived")}, false); err == nil {
		t.Fatal("archived default without replacement")
	}
	foreignSpace, err := s.CreateWorkspace(ctx, foreign, "Foreign", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpdateWorkspace(ctx, owner, first.ID, domain.WorkspaceUpdate{Status: ptr("archived"), ReplacementWorkspaceID: foreignSpace.ID}, false); err == nil {
		t.Fatal("foreign replacement accepted")
	}
	archived, err := s.UpdateWorkspace(ctx, owner, first.ID, domain.WorkspaceUpdate{Status: ptr("archived"), ReplacementWorkspaceID: second.ID}, false)
	if err != nil || archived.IsDefault {
		t.Fatalf("archive: %+v %v", archived, err)
	}
	if _, err = s.CreateConversationInWorkspace(first.ID, "Rejected archived write"); err == nil {
		t.Fatal("archived write reached persistence")
	}
	if _, err = s.UpdateWorkspace(ctx, owner, first.ID, domain.WorkspaceUpdate{MakeDefault: true}, false); err == nil {
		t.Fatal("archived default accepted")
	}
	if _, ok, err := s.GetConversationInWorkspace(first.ID, conversation.ID); err != nil || !ok {
		t.Fatal("archive removed readable resource")
	}
	if _, err = s.UpdateWorkspace(ctx, owner, first.ID, domain.WorkspaceUpdate{Status: ptr("active"), MakeDefault: true}, false); err != nil {
		t.Fatal(err)
	}
	deleted, err := s.UpdateWorkspace(ctx, owner, first.ID, domain.WorkspaceUpdate{ReplacementWorkspaceID: second.ID}, true)
	if err != nil || deleted.DeletedAt == nil {
		t.Fatalf("soft delete: %+v %v", deleted, err)
	}
	if _, err = s.GetWorkspace(ctx, owner, first.ID); !store.IsNotFound(err) {
		t.Fatalf("deleted read: %v", err)
	}
	var count int
	if err = db.QueryRow(`SELECT count(*) FROM conversations WHERE id=$1`, conversation.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("physical deletion occurred: %d %v", count, err)
	}
	if _, err = s.AddMessage(conversation.ID, "user", "late write"); err == nil {
		t.Fatal("late write to deleted space accepted")
	}
	if err = s.ProvisionPersonalWorkspace(ctx, owner); err != nil {
		t.Fatal(err)
	}
	items, err := s.ListWorkspaces(ctx, owner)
	if err != nil || len(items) != 1 || items[0].ID != second.ID || !items[0].IsDefault {
		t.Fatalf("login changed lifecycle: %+v %v", items, err)
	}
	reopened, err := store.NewPostgresStore(url)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if _, err = reopened.GetWorkspace(ctx, owner, first.ID); !store.IsNotFound(err) {
		t.Fatalf("restart restored deleted space: %v", err)
	}
}

func TestWorkspaceAdmissionAndUncertainEffectBoundary(t *testing.T) {
	s, db, _ := workspaceDatabase(t)
	ctx := t.Context()
	if err := s.InitializeWorkspaceLifecycle(ctx); err != nil {
		t.Fatal(err)
	}
	owner := workspaceUser(t, s, "admission")
	w, err := s.CreateWorkspace(ctx, owner, "Execution", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateWorkspace(ctx, owner, "Replacement", ""); err != nil {
		t.Fatal(err)
	}
	c, err := s.CreateConversationInWorkspace(w.ID, "Run")
	if err != nil {
		t.Fatal(err)
	}
	agent, _, err := s.GetDefaultAgent()
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.CreateRunWithContract(agent.ID, c.ID, domain.RuntimeSnapshot{SchemaVersion: domain.CurrentRuntimeSnapshotVersion, RunBudget: &domain.RuntimeRunBudget{}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, status := range []domain.RunStatus{domain.RunQueued, domain.RunRunning, domain.RunWaitingForUser, domain.RunFailedRecoverable, domain.RunCanceling} {
		if _, err = s.UpdateRunStatus(run.ID, status, ""); err != nil {
			t.Fatal(err)
		}
		if _, err = s.UpdateWorkspace(ctx, owner, w.ID, domain.WorkspaceUpdate{}, true); err == nil {
			t.Fatalf("closed space with %s Run", status)
		}
	}
	if _, err = s.UpdateRunStatus(run.ID, domain.RunCanceled, ""); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO tool_effects(idempotency_key,run_id,stage_id,tool_call_id,tool_name,request_hash,status,created_at,updated_at) VALUES('fixture-effect',$1,'fixture-stage','fixture-call','fixture-tool','fixture-hash','needs_reconciliation',NOW(),NOW())`, run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpdateWorkspace(ctx, owner, w.ID, domain.WorkspaceUpdate{}, true); err == nil {
		t.Fatal("uncertain effect ignored")
	}
	if _, err = db.Exec(`UPDATE tool_effects SET status='failed' WHERE idempotency_key='fixture-effect'`); err != nil {
		t.Fatal(err)
	}
	// A writer holds SHARE until its Run state is committed. Closing waits and
	// then observes that state, without wall-clock sleeps or probabilistic races.
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`UPDATE runs SET status='queued' WHERE id=$1`, run.ID); err != nil {
		t.Fatal(err)
	}
	closed := make(chan error, 1)
	go func() {
		_, err := s.UpdateWorkspace(ctx, owner, w.ID, domain.WorkspaceUpdate{Status: ptr("archived")}, false)
		closed <- err
	}()
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = <-closed; err == nil {
		t.Fatal("closure raced accepted Run")
	}
	if _, err = s.UpdateRunStatus(run.ID, domain.RunCanceled, ""); err != nil {
		t.Fatal(err)
	}
	// First switch the default, then archive. A late admission must be rejected.
	spaces, err := s.ListWorkspaces(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpdateWorkspace(ctx, owner, w.ID, domain.WorkspaceUpdate{Status: ptr("archived"), ReplacementWorkspaceID: spaces[1].ID}, false); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateRunWithContract(agent.ID, c.ID, domain.RuntimeSnapshot{SchemaVersion: domain.CurrentRuntimeSnapshotVersion, RunBudget: &domain.RuntimeRunBudget{}}, nil); err == nil {
		t.Fatal("Run accepted after archive")
	}
}

func TestWorkspaceMigrationAtomicityAndReferenceIntegrity(t *testing.T) {
	s, db, _ := workspaceDatabase(t)
	ctx := t.Context()
	a, b := workspaceUser(t, s, "migration-a"), workspaceUser(t, s, "migration-b")
	if _, err := db.Exec(`INSERT INTO auth_memberships VALUES($1,'default_workspace')`, a); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO auth_personal_workspaces(user_id,workspace_id) VALUES($1,'default_workspace')`, a); err != nil {
		t.Fatal(err)
	}
	old, err := s.CreateConversationInWorkspace("default_workspace", "Preserve default_workspace in text")
	if err != nil {
		t.Fatal(err)
	}
	message, err := s.AddMessage(old.ID, "user", "Do not replace default_workspace in content")
	if err != nil {
		t.Fatal(err)
	}
	orphan, err := s.CreateConversationInWorkspace("2", "Unknown numeric legacy namespace")
	if err != nil {
		t.Fatal(err)
	}
	stateJSON, _ := json.Marshal(domain.EmptyTaskState("default_workspace", old.ID))
	if _, err = db.Exec(`INSERT INTO task_state_revisions(id,workspace_id,conversation_id,version,previous_version,patch,state,source,created_at) VALUES('migration-state','default_workspace',$1,1,0,'{}',$2,'{}',NOW())`, old.ID, stateJSON); err != nil {
		t.Fatal(err)
	}
	preview, err := s.PreviewWorkspaceMigration(ctx, nil)
	if err != nil || len(preview) != 2 {
		t.Fatalf("preview: %+v %v", preview, err)
	}
	if err = s.ApplyWorkspaceMigration(ctx, nil); err == nil {
		t.Fatal("unowned legacy data guessed an owner")
	}
	var count int
	if err = db.QueryRow(`SELECT count(*) FROM workspaces`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("partial migration: %d %v", count, err)
	}
	if err = s.ApplyWorkspaceMigration(ctx, map[string]string{"2": b}); err != nil {
		t.Fatal(err)
	}
	if err = s.ApplyWorkspaceMigration(ctx, map[string]string{"2": b}); err != nil {
		t.Fatalf("same mapping rerun: %v", err)
	}
	if err = s.ApplyWorkspaceMigration(ctx, map[string]string{"2": a}); err == nil {
		t.Fatal("rerun changed immutable migrated owner")
	}
	converted, ok, err := s.GetConversation(old.ID)
	if err != nil || !ok || converted.WorkspaceID == "default_workspace" || converted.Title != old.Title {
		t.Fatalf("conversion: %+v %v", converted, err)
	}
	other, ok, err := s.GetConversation(orphan.ID)
	if err != nil || !ok || other.WorkspaceID == converted.WorkspaceID {
		t.Fatalf("namespace collision: %+v %v", other, err)
	}
	if _, err = s.GetWorkspace(ctx, a, converted.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.GetWorkspace(ctx, b, other.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	messages, err := s.ListMessages(old.ID)
	if err != nil || len(messages) != 1 || messages[0].ID != message.ID || messages[0].WorkspaceID != converted.WorkspaceID || messages[0].Content != message.Content {
		t.Fatalf("message integrity: %+v %v", messages, err)
	}
	state, ok, err := s.GetTaskState(old.ID)
	if err != nil || !ok || state.WorkspaceID != converted.WorkspaceID {
		t.Fatalf("state integrity: %+v %v", state, err)
	}
	if err = s.ApplyWorkspaceMigration(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO auth_memberships VALUES($1,$2)`, b, converted.WorkspaceID); err == nil {
		t.Fatal("migrated database permits shared ownership")
	}
	if _, err = db.Exec(`DELETE FROM auth_memberships WHERE user_id=$1`, a); err != nil {
		t.Fatal(err)
	}
	if err = s.ProvisionPersonalWorkspace(ctx, a); err != nil {
		t.Fatal(err)
	}
	if items, err := s.ListWorkspaces(ctx, a); err != nil || len(items) != 0 {
		t.Fatalf("revoked legacy grant restored: %+v %v", items, err)
	}
}

func TestWorkspaceMigrationRejectsSharedOrInvalidOwner(t *testing.T) {
	s, db, _ := workspaceDatabase(t)
	ctx := t.Context()
	a, b := workspaceUser(t, s, "shared-a"), workspaceUser(t, s, "shared-b")
	if _, err := db.Exec(`INSERT INTO auth_memberships VALUES($1,'shared'),($2,'shared')`, a, b); err != nil {
		t.Fatal(err)
	}
	plan, err := s.PreviewWorkspaceMigration(ctx, nil)
	if err != nil || len(plan) != 1 || plan[0].Problem == "" {
		t.Fatalf("shared preview: %+v %v", plan, err)
	}
	if err = s.ApplyWorkspaceMigration(ctx, nil); err == nil {
		t.Fatal("shared scope silently chose first owner")
	}
	if err = s.ApplyWorkspaceMigration(ctx, map[string]string{"shared": "missing-user"}); err == nil {
		t.Fatal("unknown owner accepted")
	}
	if err = s.ApplyWorkspaceMigration(ctx, map[string]string{"shared": a}); err != nil {
		t.Fatal(err)
	}
	items, err := s.ListWorkspaces(ctx, a)
	if err != nil || len(items) != 1 {
		t.Fatalf("explicit owner: %+v %v", items, err)
	}
	if items, err = s.ListWorkspaces(ctx, b); err != nil || len(items) != 0 {
		t.Fatalf("foreign grant retained: %+v %v", items, err)
	}
	var invalid *store.WorkspaceError
	_, err = s.UpdateWorkspace(ctx, a, "missing", domain.WorkspaceUpdate{}, true)
	if errors.As(err, &invalid) || !store.IsNotFound(err) {
		t.Fatalf("not-found classification: %v", err)
	}
}

func TestWorkspaceArchivePreservesRetentionAndReadability(t *testing.T) {
	s, db, _ := workspaceDatabase(t)
	ctx := t.Context()
	if err := s.InitializeWorkspaceLifecycle(ctx); err != nil {
		t.Fatal(err)
	}
	owner := workspaceUser(t, s, "retention")
	w, err := s.CreateWorkspace(ctx, owner, "Retention", "")
	if err != nil {
		t.Fatal(err)
	}
	replacement, err := s.CreateWorkspace(ctx, owner, "Replacement", "")
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.CreateConversationInWorkspace(w.ID, "Retention")
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.CreateRunWithContract("agent_planner", c.ID, domain.RuntimeSnapshot{SchemaVersion: domain.CurrentRuntimeSnapshotVersion, RunBudget: &domain.RuntimeRunBudget{}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpdateRunStatus(r.ID, domain.RunCompleted, ""); err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"messages":[]}`)
	past := time.Now().Add(-time.Minute)
	hash := store.ToolArtifactContentHash(payload)
	record := domain.ModelRequestRecord{Envelope: domain.ModelRequestEnvelope{ID: "retention-model", RunID: r.ID, ConversationID: c.ID, ModelCallID: "retention-call", Operation: "chat.completion", Provider: "fixture", Model: "fixture", RuntimeSnapshotHash: hash, PayloadHash: hash, PayloadBytes: len(payload), Parameters: map[string]any{}, SourceTokenBreakdown: map[string]int{}, CreatedAt: time.Now()}, Capture: domain.ModelRequestCapture{Mode: domain.ModelRequestCaptureFull, Content: string(payload), ContentHash: hash, OriginalBytes: len(payload), StoredBytes: len(payload), Reconstructable: true, ExpiresAt: &past}}
	if _, err = s.CreateModelRequestRecord(record); err != nil {
		t.Fatal(err)
	}
	artifact := domain.ToolArtifact{ID: "retention-artifact", SchemaVersion: domain.CurrentToolArtifactSchemaVersion, RunID: r.ID, ToolCallID: "retention-call", ToolName: "fixture", MediaType: "application/json", ContentHash: hash, OriginalByteSize: len(payload), StoredByteSize: len(payload), CreatedAt: time.Now(), ExpiresAt: &past}
	if _, err = s.CreateToolArtifact(artifact, payload); err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpdateWorkspace(ctx, owner, w.ID, domain.WorkspaceUpdate{Status: ptr("archived"), ReplacementWorkspaceID: replacement.ID}, false); err != nil {
		t.Fatal(err)
	}
	items, err := s.ListModelRequestRecords(r.ID)
	if err != nil || len(items) != 1 || !items[0].Capture.Expired || items[0].Capture.Content != "" {
		t.Fatalf("archived Capture read/retention: %+v %v", items, err)
	}
	if _, err = s.ReadToolArtifact(r.ID, artifact.ID, 0, 1); !errors.Is(err, store.ErrToolArtifactExpired) {
		t.Fatalf("archived Artifact retention: %v", err)
	}
	// The existing startup sweep runs this exact erasure statement. Reads must
	// stay unavailable after expiry even before the sweep has run.
	if _, err = db.Exec(`UPDATE tool_artifacts SET content=''::bytea WHERE expires_at<=NOW() AND octet_length(content)>0`); err != nil {
		t.Fatal(err)
	}
	var size int
	if err = db.QueryRow(`SELECT octet_length(content) FROM tool_artifacts WHERE id=$1`, artifact.ID).Scan(&size); err != nil || size != 0 {
		t.Fatalf("expired Artifact content retained: %d %v", size, err)
	}
	if _, err = db.Exec(`UPDATE tool_artifacts SET content='rewritten' WHERE id=$1`, artifact.ID); err == nil {
		t.Fatal("retention exception allowed payload resurrection")
	}
}

func ptr(value string) *string { return &value }
