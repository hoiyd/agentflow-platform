package store

import (
	"os"
	"path/filepath"
	"testing"

	"agentflow-platform/apps/api/internal/contextassembly"
	"agentflow-platform/apps/api/internal/domain"
	eventpkg "agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/skill"
	"agentflow-platform/apps/api/internal/testsupport/pgfixture"
)

func TestPostgresSkillsRoundTripAcrossRestart(t *testing.T) {
	databaseURL := pgfixture.DatabaseURL(t)
	storage, err := NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	dir := filepath.Join(t.TempDir(), "persisted-method")
	if err := os.MkdirAll(filepath.Join(dir, "references"), 0700); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"SKILL.md": "---\nname: persisted-method\ndescription: Preserve facts\n---\nImmutable instructions.", "references/check.md": "Immutable resource."} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	catalog, err := skill.LoadDirectories([]string{dir})
	if err != nil {
		t.Fatal(err)
	}
	packages, err := catalog.Freeze([]string{"persisted-method"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	workspace := testOwnedWorkspace(t, storage)
	agent, err := storage.CreateAgent(domain.Agent{WorkspaceID: workspace, Name: "Skill holder", Skills: []string{"persisted-method"}})
	if err != nil {
		t.Fatal(err)
	}
	agent.Skills = nil
	if _, err := storage.UpdateAgent(agent); err != nil {
		t.Fatal(err)
	}
	loaded, ok, err := storage.GetAgent(agent.ID)
	if err != nil || !ok || len(loaded.Skills) != 0 {
		t.Fatalf("cleared agent=%#v err=%v", loaded, err)
	}
	agent.Skills = []string{"persisted-method"}
	if _, err := storage.UpdateAgent(agent); err != nil {
		t.Fatal(err)
	}
	conversation, err := storage.CreateConversationInWorkspace(workspace, "frozen skill")
	if err != nil {
		t.Fatal(err)
	}
	snapshot := testRuntimeSnapshot()
	snapshot.Agent.ID = agent.ID
	snapshot.Agent.Skills = agent.Skills
	snapshot.Skills = packages
	run, err := storage.CreateRunWithContract(agent.ID, conversation.ID, snapshot, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := skill.WithAgent(eventpkg.WithScope(t.Context(), eventpkg.Scope{RunID: run.ID, ConversationID: conversation.ID, TurnID: "persisted-turn"}), agent.ID, "/skill:persisted-method Begin")
	session := contextassembly.Session{AgentID: agent.ID, CurrentInput: "/skill:persisted-method Begin", Config: snapshot.ContextAssembly, Sink: eventpkg.StoreSink{Store: storage}, LoadSkills: func() ([]domain.SkillSnapshot, error) { return skill.NewService(storage).Active(ctx, &snapshot) }}
	if _, err := contextassembly.Assemble(contextassembly.WithSession(ctx, session), contextassembly.Request{Model: "fixture", Messages: []contextassembly.Message{{Role: "system", Content: "Use facts."}, {Role: "user", Content: session.CurrentInput}}}); err != nil {
		t.Fatal(err)
	}
	if err := storage.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restarted.Close() })
	loaded, ok, err = restarted.GetAgent(agent.ID)
	if err != nil || !ok || len(loaded.Skills) != 1 {
		t.Fatalf("agent=%#v err=%v", loaded, err)
	}
	replay, ok, err := restarted.GetRunReplay(run.ID)
	if err != nil || !ok || replay.RuntimeSnapshot == nil {
		t.Fatalf("replay=%#v err=%v", replay, err)
	}
	frozen, err := skill.Bound(replay.RuntimeSnapshot, agent.ID)
	if err != nil || len(frozen) != 1 || frozen[0].Hash != packages[0].Hash || frozen[0].Instructions != "Immutable instructions." {
		t.Fatalf("frozen=%#v err=%v", frozen, err)
	}
	resumed := skill.WithAgent(eventpkg.WithScope(t.Context(), eventpkg.Scope{RunID: run.ID, ConversationID: conversation.ID, TurnID: "resumed-turn"}), agent.ID, "Continue")
	active, err := skill.NewService(restarted).Active(resumed, replay.RuntimeSnapshot)
	if err != nil || len(active) != 1 || active[0].Hash != packages[0].Hash {
		t.Fatalf("explicit activation round-trip=%+v err=%v", active, err)
	}
	page, err := skill.ReadResource(frozen[0], "references/check.md", 0, 512)
	if err != nil || page.Content != "Immutable resource." {
		t.Fatalf("resource=%#v err=%v", page, err)
	}
}
